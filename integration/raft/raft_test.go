/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package raft

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/hyperledger/fabric-config/configtx"
	"github.com/hyperledger/fabric-config/configtx/orderer"
	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/integration/nwo"
	"github.com/hyperledger/fabric/integration/nwo/commands"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
)

var _ = Describe("Raft", func() {
	var (
		testDir string
		client  *docker.Client
		network *nwo.Network
		process ifrit.Process
	)

	BeforeEach(func() {
		var err error
		testDir, err = ioutil.TempDir("", "e2e")
		Expect(err).NotTo(HaveOccurred())

		client, err = docker.NewClientFromEnv()
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if process != nil {
			process.Signal(syscall.SIGTERM)
			Eventually(process.Wait(), network.EventuallyTimeout).Should(Receive())
		}
		if network != nil {
			network.Cleanup()
		}
		os.RemoveAll(testDir)
	})

	Describe("basic etcdraft network without a system channel", func() {
		var ordererProcess ifrit.Process
		BeforeEach(func() {
			raftConfig := nwo.BasicEtcdRaft()
			network = nwo.New(raftConfig, testDir, client, StartPort(), components)
			network.ChannelParticipationEnabled = true
			network.GenerateConfigTree()

			orderer := network.Orderer("orderer")
			ordererConfig := network.ReadOrdererConfig(orderer)
			ordererConfig.General.BootstrapMethod = "none"
			network.WriteOrdererConfig(orderer, ordererConfig)
			network.Bootstrap()

			ordererRunner := network.OrdererRunner(orderer)
			ordererProcess = ifrit.Invoke(ordererRunner)
			Eventually(ordererProcess.Ready, network.EventuallyTimeout).Should(BeClosed())
			Eventually(ordererRunner.Err(), network.EventuallyTimeout).Should(gbytes.Say("Registrar initializing without a system channel, number of application channels: 0"))

			nwo.ChannelParticipationList(network, orderer, nil)
		})

		AfterEach(func() {
			if ordererProcess != nil {
				ordererProcess.Signal(syscall.SIGTERM)
				Eventually(ordererProcess.Wait(), network.EventuallyTimeout).Should(Receive())
			}
		})

		It("starts the orderer but rejects channel creation requests via the legacy channel creation", func() {
			By("attempting to create a channel without a system channel defined")
			sess, err := network.PeerAdminSession(network.Peer("Org1", "peer0"), commands.ChannelCreate{
				ChannelID:   "testchannel",
				Orderer:     network.OrdererAddress(network.Orderer("orderer"), nwo.ListenPort),
				File:        network.CreateChannelTxPath("testchannel"),
				OutputBlock: "/dev/null",
				ClientAuth:  network.ClientAuthRequired,
			})
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(1))
			Eventually(sess.Err, network.EventuallyTimeout).Should(gbytes.Say("channel creation request not allowed because the orderer system channel is not defined"))
		})

		It("joins application channels using the channel participation API from genesis block", func() {
			orderer := network.Orderer("orderer")
			genesisBlock := applicationChannelGenesisBlock(network, orderer, "participation-trophy")
			expectedChannelInfo := nwo.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			}

			nwo.ChannelParticipationJoin(network, orderer, "participation-trophy", genesisBlock, expectedChannelInfo)
			nwo.ChannelParticipationListOne(network, orderer, expectedChannelInfo)
		})
	})
})

func applicationChannelGenesisBlock(n *nwo.Network, o *nwo.Orderer, channel string) *common.Block {
	ordererCACert := parseOrdererCAX509Certificate(n, o)
	ordererAdminCert := parseOrdererAdminX509Certificate(n, o)
	ordererTLSCert := parseOrdererTLSX509Certificate(n, o)
	host, port := ordererHostPort(n, o)
	channelConfig := configtx.Channel{
		Orderer: configtx.Orderer{
			OrdererType: "etcdraft",
			Organizations: []configtx.Organization{
				{
					Name: "OrdererOrg",
					Policies: map[string]configtx.Policy{
						"Readers": {
							Type: "Signature",
							Rule: "OR('OrdererMSP.member')",
						},
						"Writers": {
							Type: "Signature",
							Rule: "OR('OrdererMSP.member')",
						},
						"Admins": {
							Type: "Signature",
							Rule: "OR('OrdererMSP.admin')",
						},
					},
					OrdererEndpoints: []string{
						"localhost:123",
					},
					MSP: configtx.MSP{
						Name:      "OrdererMSP",
						RootCerts: []*x509.Certificate{ordererCACert},
						Admins:    []*x509.Certificate{ordererAdminCert},
						// TLSRootCerts: []*x509.Certificate{ordererTLSCert},
					},
				},
			},
			EtcdRaft: orderer.EtcdRaft{
				Consenters: []orderer.Consenter{
					{
						Address: orderer.EtcdAddress{
							Host: host,
							Port: port,
						},
						ClientTLSCert: ordererTLSCert,
						ServerTLSCert: ordererTLSCert,
					},
				},
				Options: orderer.EtcdRaftOptions{
					TickInterval:         "500ms",
					ElectionTick:         10,
					HeartbeatTick:        1,
					MaxInflightBlocks:    5,
					SnapshotIntervalSize: 16 * 1024 * 1024, // 16 MB
				},
			},
			Policies: map[string]configtx.Policy{
				"Readers": {
					Type: "ImplicitMeta",
					Rule: "ANY Readers",
				},
				"Writers": {
					Type: "ImplicitMeta",
					Rule: "ANY Writers",
				},
				"Admins": {
					Type: "ImplicitMeta",
					Rule: "MAJORITY Admins",
				},
				"BlockValidation": {
					Type: "ImplicitMeta",
					Rule: "ANY Writers",
				},
			},
			Capabilities: []string{"V2_0"},
			BatchSize: orderer.BatchSize{
				MaxMessageCount:   100,
				AbsoluteMaxBytes:  100,
				PreferredMaxBytes: 100,
			},
			BatchTimeout: 2 * time.Second,
			State:        orderer.ConsensusStateNormal,
		},
		Application: configtx.Application{
			Capabilities: []string{"V2_0"},
			// ACLs:         map[string]string{"event/Block": "/Channel/Application/Readers"},
			Policies: map[string]configtx.Policy{
				configtx.ReadersPolicyKey: {
					Type: configtx.ImplicitMetaPolicyType,
					Rule: "ANY Readers",
				},
				configtx.WritersPolicyKey: {
					Type: configtx.ImplicitMetaPolicyType,
					Rule: "ANY Writers",
				},
				configtx.AdminsPolicyKey: {
					Type: configtx.ImplicitMetaPolicyType,
					Rule: "MAJORITY Admins",
				},
				configtx.EndorsementPolicyKey: {
					Type: configtx.ImplicitMetaPolicyType,
					Rule: "MAJORITY Endorsement",
				},
				configtx.LifecycleEndorsementPolicyKey: {
					Type: configtx.ImplicitMetaPolicyType,
					Rule: "MAJORITY Endorsement",
				},
			},
		},
		Capabilities: []string{"V2_0"},
		Policies: map[string]configtx.Policy{
			"Readers": {
				Type: "ImplicitMeta",
				Rule: "ANY Readers",
			},
			"Writers": {
				Type: "ImplicitMeta",
				Rule: "ANY Writers",
			},
			"Admins": {
				Type: "ImplicitMeta",
				Rule: "MAJORITY Admins",
			},
		},
	}

	genesisBlock, err := configtx.NewApplicationChannelGenesisBlock(channelConfig, channel)
	Expect(err).NotTo(HaveOccurred())

	return genesisBlock
}

// func parsePeerAdminX509Certificate(n *nwo.Network, p *nwo.Peer) *x509.Certificate {
// 	return parseCertificate(n.PeerUserCert(p, "Admin"))
// }

func parseOrdererAdminX509Certificate(n *nwo.Network, o *nwo.Orderer) *x509.Certificate {
	return parseCertificate(n.OrdererUserCert(o, "Admin"))
}

func parseOrdererCAX509Certificate(n *nwo.Network, o *nwo.Orderer) *x509.Certificate {
	return parseCertificate(ordererCACert(n, o))
}

func ordererCACert(n *nwo.Network, o *nwo.Orderer) string {
	org := n.Organization(o.Organization)
	Expect(org).NotTo(BeNil())

	return filepath.Join(
		n.OrdererOrgMSPDir(org),
		"cacerts",
		fmt.Sprintf("ca.%s-cert.pem", org.Domain),
	)
}

func parseOrdererTLSX509Certificate(n *nwo.Network, o *nwo.Orderer) *x509.Certificate {
	return parseCertificate(filepath.Join(n.OrdererLocalTLSDir(o), "server.crt"))
}

func parseCertificate(filename string) *x509.Certificate {
	certBytes, err := ioutil.ReadFile(filename)
	Expect(err).NotTo(HaveOccurred())
	pemBlock, _ := pem.Decode(certBytes)
	cert, err := x509.ParseCertificate(pemBlock.Bytes)
	Expect(err).NotTo(HaveOccurred())
	return cert
}

func ordererHostPort(n *nwo.Network, o *nwo.Orderer) (string, int) {
	return splitHostPort(n.OrdererAddress(o, nwo.ListenPort))
}

func splitHostPort(address string) (string, int) {
	host, port, err := net.SplitHostPort(address)
	Expect(err).NotTo(HaveOccurred())
	portInt, err := strconv.Atoi(port)
	Expect(err).NotTo(HaveOccurred())
	return host, portInt
}
