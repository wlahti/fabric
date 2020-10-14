/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package raft

import (
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	docker "github.com/fsouza/go-dockerclient"
	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-config/configtx"
	"github.com/hyperledger/fabric-config/configtx/orderer"
	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/integration/channelparticipation"
	conftx "github.com/hyperledger/fabric/integration/configtx"
	"github.com/hyperledger/fabric/integration/nwo"
	"github.com/hyperledger/fabric/integration/nwo/commands"
	"github.com/hyperledger/fabric/integration/ordererclient"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

var _ = Describe("ChannelParticipation", func() {
	var (
		testDir          string
		client           *docker.Client
		network          *nwo.Network
		ordererProcesses []ifrit.Process
		ordererRunners   []*ginkgomon.Runner
	)

	BeforeEach(func() {
		var err error
		testDir, err = ioutil.TempDir("", "channel-participation")
		Expect(err).NotTo(HaveOccurred())

		client, err = docker.NewClientFromEnv()
		Expect(err).NotTo(HaveOccurred())

		ordererProcesses = []ifrit.Process{}
		ordererRunners = []*ginkgomon.Runner{}
	})

	AfterEach(func() {
		for _, ordererProcess := range ordererProcesses {
			ordererProcess.Signal(syscall.SIGTERM)
			Eventually(ordererProcess.Wait(), network.EventuallyTimeout).Should(Receive())
		}
		if network != nil {
			network.Cleanup()
		}
		os.RemoveAll(testDir)
	})

	Describe("three node etcdraft network without a system channel", func() {
		startOrderer := func(o *nwo.Orderer) {
			ordererRunner := network.OrdererRunner(o)
			ordererProcess := ifrit.Invoke(ordererRunner)
			Eventually(ordererProcess.Ready(), network.EventuallyTimeout).Should(BeClosed())
			Eventually(ordererRunner.Err(), network.EventuallyTimeout).Should(gbytes.Say(
				"Registrar initializing without a system channel, number of application channels: 0, with 0 consensus.Chain\\(s\\) and 0 follower.Chain\\(s\\)"))
			ordererProcesses = append(ordererProcesses, ordererProcess)
			ordererRunners = append(ordererRunners, ordererRunner)
		}

		BeforeEach(func() {
			network = nwo.New(nwo.MultiNodeEtcdRaft(), testDir, client, StartPort(), components)
			network.Consensus.ChannelParticipationEnabled = true
			network.Consensus.BootstrapMethod = "none"
			network.GenerateConfigTree()
			network.Bootstrap()
		})

		It("starts an orderer but rejects channel creation requests via the legacy channel creation", func() {
			orderer1 := network.Orderer("orderer1")
			startOrderer(orderer1)

			channelparticipation.List(network, orderer1, nil)

			By("attempting to create a channel without a system channel defined")
			sess, err := network.PeerAdminSession(network.Peer("Org1", "peer0"), commands.ChannelCreate{
				ChannelID:   "testchannel",
				Orderer:     network.OrdererAddress(orderer1, nwo.ListenPort),
				File:        network.CreateChannelTxPath("testchannel"),
				OutputBlock: "/dev/null",
				ClientAuth:  network.ClientAuthRequired,
			})
			Expect(err).NotTo(HaveOccurred())
			Eventually(sess, network.EventuallyTimeout).Should(gexec.Exit(1))
			Eventually(sess.Err, network.EventuallyTimeout).Should(gbytes.Say("channel creation request not allowed because the orderer system channel is not defined"))
		})

		It("joins application channels from genesis block and removes a channel using the channel participation API", func() {
			orderer1 := network.Orderer("orderer1")
			orderer2 := network.Orderer("orderer2")
			orderer3 := network.Orderer("orderer3")
			orderers := []*nwo.Orderer{orderer1, orderer2, orderer3}
			members := []*nwo.Orderer{orderer1, orderer2}
			peer := network.Peer("Org1", "peer0")

			By("starting all three orderers")
			for _, o := range orderers {
				startOrderer(o)
				channelparticipation.List(network, o, nil)
			}

			genesisBlock := applicationChannelGenesisBlock(network, members, peer, "participation-trophy")
			expectedChannelInfoPT := channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			}

			for _, o := range members {
				By("joining " + o.Name + " to channel as a member")
				channelparticipation.Join(network, o, "participation-trophy", genesisBlock, expectedChannelInfoPT)
				channelInfo := channelparticipation.ListOne(network, o, "participation-trophy")
				Expect(channelInfo).To(Equal(expectedChannelInfoPT))
			}

			By("waiting for the leader to be ready")
			memberRunners := []*ginkgomon.Runner{ordererRunners[0], ordererRunners[1]}
			leader := findLeader(memberRunners)

			submitTxn(orderer1, peer, network, members, 1, channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          2,
			})

			submitTxn(orderer2, peer, network, members, 2, channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          3,
			})

			By("joining orderer3 to the channel as a follower")
			// make sure we can join using a config block from one of the other orderers
			configBlockPT := nwo.GetConfigBlock(network, peer, orderer2, "participation-trophy")
			expectedChannelInfoPTFollower := channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "onboarding",
				ClusterRelation: "follower",
				Height:          0,
			}
			channelparticipation.Join(network, orderer3, "participation-trophy", configBlockPT, expectedChannelInfoPTFollower)

			By("ensuring orderer3 completes onboarding successfully")
			expectedChannelInfoPTFollower.Status = "active"
			expectedChannelInfoPTFollower.Height = 3
			Eventually(func() channelparticipation.ChannelInfo {
				return channelparticipation.ListOne(network, orderer3, "participation-trophy")
			}, network.EventuallyTimeout).Should(Equal(expectedChannelInfoPTFollower))

			By("adding orderer3 to the consenters set")
			channelConfig := nwo.GetConfig(network, peer, orderer1, "participation-trophy")
			c := configtx.New(channelConfig)
			err := c.Orderer().AddConsenter(consenterChannelConfig(network, orderer3))
			Expect(err).NotTo(HaveOccurred())
			computeSignSubmitConfigUpdate(network, orderer1, peer, c, "participation-trophy")

			By("ensuring orderer3 transitions from follower to member")
			// config update above added a block
			expectedChannelInfoPT.Height = 4
			Eventually(func() channelparticipation.ChannelInfo {
				return channelparticipation.ListOne(network, orderer3, "participation-trophy")
			}, network.EventuallyTimeout).Should(Equal(expectedChannelInfoPT))

			// make sure orderer3 finds the leader
			newLeader := findLeader([]*ginkgomon.Runner{ordererRunners[2]})
			Expect(newLeader).To(Equal(leader))

			By("submitting transaction to orderer3 to ensure it is active")
			env := CreateBroadcastEnvelope(network, peer, "participation-trophy", []byte("hello-again"))
			resp, err := ordererclient.Broadcast(network, orderer3, env)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal(common.Status_SUCCESS))
			expectedBlockNumPerChannel := map[string]int{"participation-trophy": 4}
			assertBlockReception(expectedBlockNumPerChannel, orderers, peer, network)

			By("joining orderer1 to another channel as a member")
			genesisBlockAPT := applicationChannelGenesisBlock(network, []*nwo.Orderer{orderer1}, peer, "another-participation-trophy")
			expectedChannelInfoAPT := channelparticipation.ChannelInfo{
				Name:            "another-participation-trophy",
				URL:             "/participation/v1/channels/another-participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			}
			channelparticipation.Join(network, orderer1, "another-participation-trophy", genesisBlockAPT, expectedChannelInfoAPT)
			channelInfo := channelparticipation.ListOne(network, orderer1, "another-participation-trophy")
			Expect(channelInfo).To(Equal(expectedChannelInfoAPT))

			By("listing all channels for orderer1")
			channelparticipation.List(network, orderer1, []string{"participation-trophy", "another-participation-trophy"})

			By("removing orderer1 from the consenter set")
			channelConfig = nwo.GetConfig(network, peer, orderer2, "participation-trophy")
			c = configtx.New(channelConfig)
			err = c.Orderer().RemoveConsenter(consenterChannelConfig(network, orderer1))
			Expect(err).NotTo(HaveOccurred())
			computeSignSubmitConfigUpdate(network, orderer2, peer, c, "participation-trophy")

			// remove orderer1 from the orderer runners
			ordererRunners = ordererRunners[1:]
			if leader == 1 {
				By("waiting for the new leader to be ready")
				newLeader := findLeader(ordererRunners)
				Expect(newLeader).NotTo(Equal(leader))
			}

			By("removing orderer1 from a channel")
			channelparticipation.Remove(network, orderer1, "participation-trophy")

			By("listing all channels for orderer1")
			channelparticipation.List(network, orderer1, []string{"another-participation-trophy"})

			By("ensuring the channel is still usable by submitting a transaction to each remaining consenter for the channel")
			orderers = []*nwo.Orderer{orderer2, orderer3}

			submitTxn(orderer2, peer, network, orderers, 5, channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          6,
			})

			submitTxn(orderer3, peer, network, orderers, 6, channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          7,
			})

			By("attempting to join with an invalid block")
			channelparticipationJoinFailure(network, orderer3, "nice-try", &common.Block{}, http.StatusBadRequest, "invalid join block: block is not a config block")

			By("attempting to join a channel that already exists")
			channelparticipationJoinFailure(network, orderer3, "participation-trophy", genesisBlock, http.StatusMethodNotAllowed, "cannot join: channel already exists")

			By("attempting to join system channel when app channels already exist")
			systemChannelBlockBytes, err := ioutil.ReadFile(network.OutputBlockPath(network.SystemChannel.Name))
			Expect(err).NotTo(HaveOccurred())
			systemChannelBlock := &common.Block{}
			err = proto.Unmarshal(systemChannelBlockBytes, systemChannelBlock)
			Expect(err).NotTo(HaveOccurred())
			channelparticipationJoinFailure(network, orderer3, "systemchannel", systemChannelBlock, http.StatusForbidden, "cannot join: application channels already exist")
		})

		It("join application channel with join-block as member via channel participation api", func() {
			orderer1 := network.Orderer("orderer1")
			orderer2 := network.Orderer("orderer2")
			orderer3 := network.Orderer("orderer3")
			orderers := []*nwo.Orderer{orderer1, orderer2}
			peer := network.Peer("Org1", "peer0")

			By("starting two orderers")
			for _, o := range orderers {
				startOrderer(o)
				channelparticipation.List(network, o, nil)
			}

			genesisBlock := applicationChannelGenesisBlock(network, orderers, peer, "participation-trophy")
			expectedChannelInfoPT := channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			}

			for _, o := range orderers {
				By("joining " + o.Name + " to channel as a member")
				channelparticipation.Join(network, o, "participation-trophy", genesisBlock, expectedChannelInfoPT)
				channelInfo := channelparticipation.ListOne(network, o, "participation-trophy")
				Expect(channelInfo).To(Equal(expectedChannelInfoPT))
			}

			By("waiting for the leader to be ready")
			memberRunners := []*ginkgomon.Runner{ordererRunners[0], ordererRunners[1]}
			leader := findLeader(memberRunners)

			submitTxn(orderer1, peer, network, orderers, 1, channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          2,
			})

			submitTxn(orderer2, peer, network, orderers, 2, channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          3,
			})

			By("submitting a channel config update")
			channelConfig := nwo.GetConfig(network, peer, orderer1, "participation-trophy")
			c := configtx.New(channelConfig)
			err := c.Orderer().AddCapability("V1_1")
			Expect(err).NotTo(HaveOccurred())
			computeSignSubmitConfigUpdate(network, orderer1, peer, c, "participation-trophy")

			currentBlockNumber := nwo.CurrentConfigBlockNumber(network, peer, orderer1, "participation-trophy")
			Expect(currentBlockNumber).To(BeNumerically(">", 1))

			By("starting third orderer")
			startOrderer(orderer3)
			channelparticipation.List(network, orderer3, nil)

			By("adding orderer3 to the consenters set")
			channelConfig = nwo.GetConfig(network, peer, orderer2, "participation-trophy")
			c = configtx.New(channelConfig)
			err = c.Orderer().AddConsenter(consenterChannelConfig(network, orderer3))
			Expect(err).NotTo(HaveOccurred())
			computeSignSubmitConfigUpdate(network, orderer2, peer, c, "participation-trophy")

			By("joining orderer3 to the channel as a member")
			// make sure we can join using a config block from one of the other orderers
			configBlockPT := nwo.GetConfigBlock(network, peer, orderer2, "participation-trophy")
			expectedChannelInfoMember := channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "onboarding",
				ClusterRelation: "member",
				Height:          0,
			}
			channelparticipation.Join(network, orderer3, "participation-trophy", configBlockPT, expectedChannelInfoMember)

			By("ensuring orderer3 completes onboarding successfully")
			expectedChannelInfoMember.Status = "active"
			expectedChannelInfoMember.Height = 5
			Eventually(func() channelparticipation.ChannelInfo {
				return channelparticipation.ListOne(network, orderer3, "participation-trophy")
			}, network.EventuallyTimeout).Should(Equal(expectedChannelInfoMember))

			// make sure orderer3 finds the leader
			newLeader := findLeader([]*ginkgomon.Runner{ordererRunners[2]})
			Expect(newLeader).To(Equal(leader))

			By("submitting transaction to orderer3 to ensure it is active")
			env := CreateBroadcastEnvelope(network, peer, "participation-trophy", []byte("hello-again"))
			resp, err := ordererclient.Broadcast(network, orderer3, env)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal(common.Status_SUCCESS))
			expectedBlockNumPerChannel := map[string]int{"participation-trophy": 5}
			assertBlockReception(expectedBlockNumPerChannel, orderers, peer, network)

			By("checking the channel height")
			expectedChannelInfoPT.Height = 6
			channelInfo := channelparticipation.ListOne(network, orderer3, "participation-trophy")
			Expect(channelInfo).To(Equal(expectedChannelInfoPT))
		})

		It("join application channel with join-block as follower via channel participation api", func() {
			orderer1 := network.Orderer("orderer1")
			orderer2 := network.Orderer("orderer2")
			orderer3 := network.Orderer("orderer3")
			orderers := []*nwo.Orderer{orderer1, orderer2}
			peer := network.Peer("Org1", "peer0")

			By("starting two orderers")
			for _, o := range orderers {
				startOrderer(o)
				channelparticipation.List(network, o, nil)
			}

			genesisBlock := applicationChannelGenesisBlock(network, orderers, peer, "participation-trophy")
			expectedChannelInfoPT := channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			}

			for _, o := range orderers {
				By("joining " + o.Name + " to channel as a member")
				channelparticipation.Join(network, o, "participation-trophy", genesisBlock, expectedChannelInfoPT)
				channelInfo := channelparticipation.ListOne(network, o, "participation-trophy")
				Expect(channelInfo).To(Equal(expectedChannelInfoPT))
			}

			By("waiting for the leader to be ready")
			memberRunners := []*ginkgomon.Runner{ordererRunners[0], ordererRunners[1]}
			leader := findLeader(memberRunners)

			submitTxn(orderer1, peer, network, orderers, 1, channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          2,
			})

			submitTxn(orderer2, peer, network, orderers, 2, channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          3,
			})

			By("submitting a channel config update")
			channelConfig := nwo.GetConfig(network, peer, orderer1, "participation-trophy")
			c := configtx.New(channelConfig)
			err := c.Orderer().AddCapability("V1_1")
			Expect(err).NotTo(HaveOccurred())
			computeSignSubmitConfigUpdate(network, orderer1, peer, c, "participation-trophy")

			currentBlockNumber := nwo.CurrentConfigBlockNumber(network, peer, orderer1, "participation-trophy")
			Expect(currentBlockNumber).To(BeNumerically(">", 1))

			By("starting third orderer")
			startOrderer(orderer3)
			channelparticipation.List(network, orderer3, nil)

			By("joining orderer3 to the channel as a follower")
			// make sure we can join using a config block from one of the other orderers
			configBlockPT := nwo.GetConfigBlock(network, peer, orderer2, "participation-trophy")
			expectedChannelInfoPTFollower := channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "onboarding",
				ClusterRelation: "follower",
				Height:          0,
			}
			channelparticipation.Join(network, orderer3, "participation-trophy", configBlockPT, expectedChannelInfoPTFollower)

			By("ensuring orderer3 completes onboarding successfully")
			expectedChannelInfoPTFollower.Status = "active"
			expectedChannelInfoPTFollower.Height = 4
			Eventually(func() channelparticipation.ChannelInfo {
				return channelparticipation.ListOne(network, orderer3, "participation-trophy")
			}, network.EventuallyTimeout).Should(Equal(expectedChannelInfoPTFollower))

			By("adding orderer3 to the consenters set")
			channelConfig = nwo.GetConfig(network, peer, orderer1, "participation-trophy")
			c = configtx.New(channelConfig)
			err = c.Orderer().AddConsenter(consenterChannelConfig(network, orderer3))
			Expect(err).NotTo(HaveOccurred())
			computeSignSubmitConfigUpdate(network, orderer1, peer, c, "participation-trophy")

			By("ensuring orderer3 transitions from follower to member")
			// config update above added a block
			expectedChannelInfoPT.Height = 5
			Eventually(func() channelparticipation.ChannelInfo {
				return channelparticipation.ListOne(network, orderer3, "participation-trophy")
			}, network.EventuallyTimeout).Should(Equal(expectedChannelInfoPT))

			// make sure orderer3 finds the leader
			newLeader := findLeader([]*ginkgomon.Runner{ordererRunners[2]})
			Expect(newLeader).To(Equal(leader))

			submitTxn(orderer3, peer, network, orderers, 5, channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          6,
			})
		})

	})

	Describe("three node etcdraft network with a system channel", func() {
		startOrderer := func(o *nwo.Orderer) {
			ordererRunner := network.OrdererRunner(o)
			ordererProcess := ifrit.Invoke(ordererRunner)
			Eventually(ordererProcess.Ready(), network.EventuallyTimeout).Should(BeClosed())
			ordererProcesses = append(ordererProcesses, ordererProcess)
			ordererRunners = append(ordererRunners, ordererRunner)
		}

		restartOrderer := func(o *nwo.Orderer, index int) {
			ordererProcesses[index].Signal(syscall.SIGKILL)
			Eventually(ordererProcesses[index].Wait(), network.EventuallyTimeout).Should(Receive(MatchError("exit status 137")))
			ordererRunner := network.OrdererRunner(o)
			ordererProcess := ifrit.Invoke(ordererRunner)
			Eventually(ordererProcess.Ready(), network.EventuallyTimeout).Should(BeClosed())
			ordererProcesses[index] = ordererProcess
			ordererRunners[index] = ordererRunner
		}

		BeforeEach(func() {
			network = nwo.New(nwo.MultiNodeEtcdRaft(), testDir, client, StartPort(), components)
			network.GenerateConfigTree()
			network.Bootstrap()
		})

		It("joins channels using the legacy channel creation mechanism and then removes the system channel to transition to the channel participation API", func() {
			orderer1 := network.Orderer("orderer1")
			orderer2 := network.Orderer("orderer2")
			orderer3 := network.Orderer("orderer3")
			orderers := []*nwo.Orderer{orderer1, orderer2, orderer3}
			peer := network.Peer("Org1", "peer0")
			for _, o := range orderers {
				startOrderer(o)
			}

			// Replace with listing logic
			findLeader(ordererRunners)

			By("creating an application channel using system channel")
			network.CreateChannel("testchannel", orderer1, peer)

			By("broadcasting envelopes to each orderer")
			for _, o := range orderers {
				env := CreateBroadcastEnvelope(network, peer, "testchannel", []byte("hello"))
				resp, err := ordererclient.Broadcast(network, o, env)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Status).To(Equal(common.Status_SUCCESS))
			}

			By("enabling the channel participation API on each orderer")
			network.Consensus.ChannelParticipationEnabled = true
			network.Consensus.BootstrapMethod = "none"
			for i, o := range orderers {
				network.GenerateOrdererConfig(o)
				restartOrderer(o, i)
			}

			By("finding the leader (once for system channel and once for application channel)")
			findLeader(ordererRunners)
			findLeader(ordererRunners)

			By("listing the channels")
			expectedChannelInfo := channelparticipation.ChannelInfo{
				Name:            "testchannel",
				URL:             "/participation/v1/channels/testchannel",
				Status:          "active",
				ClusterRelation: "member",
				Height:          4,
			}
			for _, o := range orderers {
				By("listing single channel")
				Eventually(func() channelparticipation.ChannelInfo {
					return channelparticipation.ListOne(network, o, "testchannel")
				}, network.EventuallyTimeout).Should(Equal(expectedChannelInfo))
				By("listing all channels")
				channelparticipation.List(network, o, []string{"testchannel"}, "systemchannel")
			}

			By("attempting to join a channel when the system channel is present")
			genesisBlock := applicationChannelGenesisBlock(network, orderers, peer, "participation-trophy")
			channelparticipationJoinFailure(network, orderers[0], "participation-trophy", genesisBlock, http.StatusMethodNotAllowed, "cannot join: system channel exists")

			By("attempting to remove a channel when the system channel is present")
			channelparticipationRemoveFailure(network, orderers[0], "testchannel", http.StatusMethodNotAllowed, "cannot remove: system channel exists")

			By("putting the system channel into maintenance mode")
			channelConfig := nwo.GetConfig(network, peer, orderer2, "systemchannel")
			c := configtx.New(channelConfig)
			err := c.Orderer().SetConsensusState(orderer.ConsensusStateMaintenance)
			Expect(err).NotTo(HaveOccurred())
			computeSignSubmitConfigUpdate(network, orderer2, peer, c, "systemchannel")

			By("removing the system channel with the channel participation API")
			for _, o := range orderers {
				channelparticipation.Remove(network, o, "systemchannel")
			}
			By("finding the leader")
			findLeader(ordererRunners)

			By("listing the channels again")
			for _, o := range orderers {
				channelparticipation.List(network, o, []string{"testchannel"})
			}

			By("broadcasting envelopes to each orderer")
			for _, o := range orderers {
				env := CreateBroadcastEnvelope(network, peer, "testchannel", []byte("hello"))
				resp, err := ordererclient.Broadcast(network, o, env)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Status).To(Equal(common.Status_SUCCESS))
			}

			By("using the channel participation API to join a new channel")
			expectedChannelInfoPT := channelparticipation.ChannelInfo{
				Name:            "participation-trophy",
				URL:             "/participation/v1/channels/participation-trophy",
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			}

			for _, o := range orderers {
				By("joining " + o.Name + " to channel as a member")
				channelparticipation.Join(network, o, "participation-trophy", genesisBlock, expectedChannelInfoPT)
				channelInfo := channelparticipation.ListOne(network, o, "participation-trophy")
				Expect(channelInfo).To(Equal(expectedChannelInfoPT))
			}

			By("waiting for the leader to be ready")
			findLeader(ordererRunners)

			By("ensuring the channel is usable by submitting a transaction to each member")
			env := CreateBroadcastEnvelope(network, peer, "participation-trophy", []byte("hello"))
			for _, o := range orderers {
				By("submitting transaction to " + o.Name)
				expectedChannelInfoPT.Height++
				resp, err := ordererclient.Broadcast(network, o, env)
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Status).To(Equal(common.Status_SUCCESS))
				expectedBlockNumPerChannel := map[string]int{"participation-trophy": int(expectedChannelInfoPT.Height - 1)}
				assertBlockReception(expectedBlockNumPerChannel, orderers, peer, network)

				By("checking the channel height")
				channelInfo := channelparticipation.ListOne(network, o, "participation-trophy")
				Expect(channelInfo).To(Equal(expectedChannelInfoPT))
			}
		})
	})

	Describe("create system channel on empty network", func() {
		startOrderer := func(o *nwo.Orderer) {
			ordererRunner := network.OrdererRunner(o)
			ordererProcess := ifrit.Invoke(ordererRunner)
			Eventually(ordererProcess.Ready(), network.EventuallyTimeout).Should(BeClosed())
			ordererProcesses = append(ordererProcesses, ordererProcess)
			ordererRunners = append(ordererRunners, ordererRunner)
		}

		restartOrderer := func(o *nwo.Orderer, index int) {
			ordererProcesses[index].Signal(syscall.SIGKILL)
			Eventually(ordererProcesses[index].Wait(), network.EventuallyTimeout).Should(Receive(MatchError("exit status 137")))
			ordererRunner := network.OrdererRunner(o)
			ordererProcess := ifrit.Invoke(ordererRunner)
			Eventually(ordererProcess.Ready(), network.EventuallyTimeout).Should(BeClosed())
			ordererProcesses[index] = ordererProcess
			ordererRunners[index] = ordererRunner
		}

		BeforeEach(func() {
			network = nwo.New(nwo.MultiNodeEtcdRaft(), testDir, client, StartPort(), components)
			network.Consensus.ChannelParticipationEnabled = true
			network.Consensus.BootstrapMethod = "none"
			network.GenerateConfigTree()
			network.Bootstrap()
		})

		It("Creating the system channel with a genesis block, so no onboarding is needed", func() {
			orderer1 := network.Orderer("orderer1")
			orderer2 := network.Orderer("orderer2")
			orderer3 := network.Orderer("orderer3")
			orderers := []*nwo.Orderer{orderer1, orderer2, orderer3}
			for _, o := range orderers {
				startOrderer(o)
			}

			systemChannelBlockBytes, err := ioutil.ReadFile(network.OutputBlockPath(network.SystemChannel.Name))
			Expect(err).NotTo(HaveOccurred())
			systemChannelBlock := &common.Block{}
			err = proto.Unmarshal(systemChannelBlockBytes, systemChannelBlock)
			Expect(err).NotTo(HaveOccurred())

			expectedChannelInfoPT := channelparticipation.ChannelInfo{
				Name:            network.SystemChannel.Name,
				URL:             fmt.Sprintf("/participation/v1/channels/%s", network.SystemChannel.Name),
				Status:          "inactive",
				ClusterRelation: "member",
				Height:          1,
			}

			channelparticipation.Join(network, orderer1, network.SystemChannel.Name, systemChannelBlock, expectedChannelInfoPT)
			channelparticipation.Join(network, orderer2, network.SystemChannel.Name, systemChannelBlock, expectedChannelInfoPT)
			channelparticipation.Join(network, orderer3, network.SystemChannel.Name, systemChannelBlock, expectedChannelInfoPT)

			for i, o := range orderers {
				restartOrderer(o, i)
			}

			findLeader(ordererRunners)

			By("listing the channels")
			expectedChannelInfo := channelparticipation.ChannelInfo{
				Name:            network.SystemChannel.Name,
				URL:             fmt.Sprintf("/participation/v1/channels/%s", network.SystemChannel.Name),
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			}
			for _, o := range orderers {
				By("listing single channel")
				Eventually(func() channelparticipation.ChannelInfo {
					return channelparticipation.ListOne(network, o, network.SystemChannel.Name)
				}, network.EventuallyTimeout).Should(Equal(expectedChannelInfo))
			}
		})

		FIt("Creating the system channel with config block with number >0, when there are already channels referenced (created) by it, such that on boarding is needed for both the system channel and additional channels.", func() {
			// It("Creating the system channel with config block with number >0, when there are already channels referenced (created) by it, such that on boarding is needed for both the system channel and additional channels.", func() {
			orderer1 := network.Orderer("orderer1")
			orderer2 := network.Orderer("orderer2")
			orderer3 := network.Orderer("orderer3")
			orderers := []*nwo.Orderer{orderer1, orderer2, orderer3}
			peer := network.Peer("Org1", "peer0")
			for _, o := range orderers {
				startOrderer(o)
			}

			systemChannelBlockBytes, err := ioutil.ReadFile(network.OutputBlockPath(network.SystemChannel.Name))
			Expect(err).NotTo(HaveOccurred())
			systemChannelBlock := &common.Block{}
			err = proto.Unmarshal(systemChannelBlockBytes, systemChannelBlock)
			Expect(err).NotTo(HaveOccurred())

			expectedChannelInfoPT := channelparticipation.ChannelInfo{
				Name:            network.SystemChannel.Name,
				URL:             fmt.Sprintf("/participation/v1/channels/%s", network.SystemChannel.Name),
				Status:          "inactive",
				ClusterRelation: "member",
				Height:          1,
			}

			channelparticipation.Join(network, orderer1, network.SystemChannel.Name, systemChannelBlock, expectedChannelInfoPT)
			channelparticipation.Join(network, orderer2, network.SystemChannel.Name, systemChannelBlock, expectedChannelInfoPT)

			for i, o := range []*nwo.Orderer{orderer1, orderer2} {
				restartOrderer(o, i)
			}

			// findLeader(ordererRunners[:1])

			By("listing the channels")
			expectedChannelInfo := channelparticipation.ChannelInfo{
				Name:            network.SystemChannel.Name,
				URL:             fmt.Sprintf("/participation/v1/channels/%s", network.SystemChannel.Name),
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			}
			for _, o := range []*nwo.Orderer{orderer1, orderer2} {
				By("listing single channel")
				Eventually(func() channelparticipation.ChannelInfo {
					return channelparticipation.ListOne(network, o, network.SystemChannel.Name)
				}, network.EventuallyTimeout).Should(Equal(expectedChannelInfo))
			}

			By("updating system channel config")
			channelConfig := nwo.GetConfig(network, peer, orderer1, network.SystemChannel.Name)
			c := configtx.New(channelConfig)
			err = c.Orderer().AddCapability("V1_1")
			Expect(err).NotTo(HaveOccurred())
			computeSignSubmitConfigUpdate(network, orderer1, peer, c, network.SystemChannel.Name)

			By("fetching config block")
			configBlockPT := nwo.GetConfigBlock(network, peer, orderer2, network.SystemChannel.Name)
			fmt.Printf("!!!ARGH %v", configBlockPT.Header.Number)
			expectedChannelInfoMember := channelparticipation.ChannelInfo{
				Name:            network.SystemChannel.Name,
				URL:             fmt.Sprintf("/participation/v1/channels/%s", network.SystemChannel.Name),
				Status:          "inactive",
				ClusterRelation: "member",
				Height:          0,
			}

			By("join orderer3 to system channel")
			channelparticipation.Join(network, orderer3, network.SystemChannel.Name, configBlockPT, expectedChannelInfoMember)

			By("restarting orderer3")
			restartOrderer(orderer3, 2)

			By("listing the channels")
			expectedChannelInfo = channelparticipation.ChannelInfo{
				Name:            network.SystemChannel.Name,
				URL:             fmt.Sprintf("/participation/v1/channels/%s", network.SystemChannel.Name),
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			}
			for _, o := range orderers {
				By("listing single channel")
				Eventually(func() channelparticipation.ChannelInfo {
					return channelparticipation.ListOne(network, o, network.SystemChannel.Name)
				}, network.EventuallyTimeout).Should(Equal(expectedChannelInfo))
			}

			network.CreateChannel("testchannel1", orderer1, peer)
			network.CreateChannel("testchannel1", orderer3, peer)
			network.CreateChannel("testchannel2", orderer3, peer)
			network.CreateChannel("testchannel3", orderer2, peer)

			submitTxn(orderer1, peer, network, orderers, 1, channelparticipation.ChannelInfo{
				Name:            "testchannel1",
				URL:             fmt.Sprintf("/participation/v1/channels/%s", "testchannel1"),
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			})

			submitTxn(orderer3, peer, network, orderers, 1, channelparticipation.ChannelInfo{
				Name:            "testchannel1",
				URL:             fmt.Sprintf("/participation/v1/channels/%s", "testchannel1"),
				Status:          "active",
				ClusterRelation: "member",
				Height:          2,
			})

			submitTxn(orderer3, peer, network, orderers, 1, channelparticipation.ChannelInfo{
				Name:            "testchannel2",
				URL:             fmt.Sprintf("/participation/v1/channels/%s", "testchannel2"),
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			})

			submitTxn(orderer2, peer, network, orderers, 1, channelparticipation.ChannelInfo{
				Name:            "testchannel3",
				URL:             fmt.Sprintf("/participation/v1/channels/%s", "testchannel3"),
				Status:          "active",
				ClusterRelation: "member",
				Height:          1,
			})

		})
	})
})

func submitTxn(o *nwo.Orderer, peer *nwo.Peer, network *nwo.Network, orderers []*nwo.Orderer,
	expectedBlkNum int, expectedChannelInfo channelparticipation.ChannelInfo) {
	By("submitting a transaction to " + o.Name)
	env := CreateBroadcastEnvelope(network, peer, expectedChannelInfo.Name, []byte("hello"))
	resp, err := ordererclient.Broadcast(network, o, env)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.Status).To(Equal(common.Status_SUCCESS))
	expectedBlockNumPerChannel := map[string]int{"participation-trophy": expectedBlkNum}
	assertBlockReception(expectedBlockNumPerChannel, orderers, peer, network)

	By("checking the channel height")
	channelInfo := channelparticipation.ListOne(network, o, expectedChannelInfo.Name)
	Expect(channelInfo).To(Equal(expectedChannelInfo))
}

func applicationChannelGenesisBlock(n *nwo.Network, orderers []*nwo.Orderer, p *nwo.Peer, channel string) *common.Block {
	ordererOrgs, consenters := ordererOrganizationsAndConsenters(n, orderers)
	peerOrgs := peerOrganizations(n, p)

	channelConfig := configtx.Channel{
		Orderer: configtx.Orderer{
			OrdererType:   "etcdraft",
			Organizations: ordererOrgs,
			EtcdRaft: orderer.EtcdRaft{
				Consenters: consenters,
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
				AbsoluteMaxBytes:  1024 * 1024,
				PreferredMaxBytes: 512 * 1024,
			},
			BatchTimeout: 2 * time.Second,
			State:        "STATE_NORMAL",
		},
		Application: configtx.Application{
			Organizations: peerOrgs,
			Capabilities:  []string{"V2_0"},
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
				"Endorsement": {
					Type: "ImplicitMeta",
					Rule: "MAJORITY Endorsement",
				},
				"LifecycleEndorsement": {
					Type: "ImplicitMeta",
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

// parseCertificate loads the PEM-encoded x509 certificate at the specified
// path.
func parseCertificate(path string) *x509.Certificate {
	certBytes, err := ioutil.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	pemBlock, _ := pem.Decode(certBytes)
	cert, err := x509.ParseCertificate(pemBlock.Bytes)
	Expect(err).NotTo(HaveOccurred())
	return cert
}

// parsePrivateKey loads the PEM-encoded private key at the specified path.
func parsePrivateKey(path string) crypto.PrivateKey {
	pkBytes, err := ioutil.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	pemBlock, _ := pem.Decode(pkBytes)
	privateKey, err := x509.ParsePKCS8PrivateKey(pemBlock.Bytes)
	Expect(err).NotTo(HaveOccurred())
	return privateKey
}

func ordererOrganizationsAndConsenters(n *nwo.Network, orderers []*nwo.Orderer) ([]configtx.Organization, []orderer.Consenter) {
	ordererOrgsMap := map[string]*configtx.Organization{}
	consenters := make([]orderer.Consenter, len(orderers))

	for i, o := range orderers {
		rootCert := parseCertificate(n.OrdererCACert(o))
		adminCert := parseCertificate(n.OrdererUserCert(o, "Admin"))
		tlsRootCert := parseCertificate(filepath.Join(n.OrdererLocalTLSDir(o), "ca.crt"))

		orgConfig, ok := ordererOrgsMap[o.Organization]
		if !ok {
			orgConfig := configtxOrganization(n.Organization(o.Organization), rootCert, adminCert, tlsRootCert)
			orgConfig.OrdererEndpoints = []string{
				n.OrdererAddress(o, nwo.ListenPort),
			}
			ordererOrgsMap[o.Organization] = orgConfig
		} else {
			orgConfig.OrdererEndpoints = append(orgConfig.OrdererEndpoints, n.OrdererAddress(o, nwo.ListenPort))
			orgConfig.MSP.RootCerts = append(orgConfig.MSP.RootCerts, rootCert)
			orgConfig.MSP.Admins = append(orgConfig.MSP.Admins, adminCert)
			orgConfig.MSP.TLSRootCerts = append(orgConfig.MSP.TLSRootCerts, tlsRootCert)
		}

		consenters[i] = consenterChannelConfig(n, o)
	}

	ordererOrgs := []configtx.Organization{}
	for _, o := range ordererOrgsMap {
		ordererOrgs = append(ordererOrgs, *o)
	}

	return ordererOrgs, consenters
}

func peerOrganizations(n *nwo.Network, p *nwo.Peer) []configtx.Organization {
	rootCert := parseCertificate(n.PeerCACert(p))
	adminCert := parseCertificate(n.PeerUserCert(p, "Admin"))
	tlsRootCert := parseCertificate(filepath.Join(n.PeerLocalTLSDir(p), "ca.crt"))

	peerOrg := configtxOrganization(n.Organization(p.Organization), rootCert, adminCert, tlsRootCert)

	return []configtx.Organization{*peerOrg}
}

func configtxOrganization(org *nwo.Organization, rootCert, adminCert, tlsRootCert *x509.Certificate) *configtx.Organization {
	orgConfig := &configtx.Organization{
		Name: org.Name,
		Policies: map[string]configtx.Policy{
			"Readers": {
				Type: "Signature",
				Rule: fmt.Sprintf("OR('%s.member')", org.MSPID),
			},
			"Writers": {
				Type: "Signature",
				Rule: fmt.Sprintf("OR('%s.member')", org.MSPID),
			},
			"Admins": {
				Type: "Signature",
				Rule: fmt.Sprintf("OR('%s.member')", org.MSPID),
			},
		},
		MSP: configtx.MSP{
			Name:         org.MSPID,
			RootCerts:    []*x509.Certificate{rootCert},
			Admins:       []*x509.Certificate{adminCert},
			TLSRootCerts: []*x509.Certificate{tlsRootCert},
		},
	}

	return orgConfig
}

func computeSignSubmitConfigUpdate(n *nwo.Network, o *nwo.Orderer, p *nwo.Peer, c configtx.ConfigTx, channel string) {
	configUpdate, err := c.ComputeMarshaledUpdate(channel)
	Expect(err).NotTo(HaveOccurred())

	signingIdentity := configtx.SigningIdentity{
		Certificate: parseCertificate(n.OrdererUserCert(o, "Admin")),
		PrivateKey:  parsePrivateKey(n.OrdererUserKey(o, "Admin")),
		MSPID:       n.Organization(o.Organization).MSPID,
	}
	signature, err := signingIdentity.CreateConfigSignature(configUpdate)
	Expect(err).NotTo(HaveOccurred())

	configUpdateEnvelope, err := configtx.NewEnvelope(configUpdate, signature)
	Expect(err).NotTo(HaveOccurred())
	err = signingIdentity.SignEnvelope(configUpdateEnvelope)
	Expect(err).NotTo(HaveOccurred())

	currentBlockNumber := nwo.CurrentConfigBlockNumber(n, p, o, channel)

	resp, err := ordererclient.Broadcast(n, o, configUpdateEnvelope)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.Status).To(Equal(common.Status_SUCCESS))

	ccb := func() uint64 { return nwo.CurrentConfigBlockNumber(n, p, o, channel) }
	Eventually(ccb, n.EventuallyTimeout).Should(BeNumerically(">", currentBlockNumber))
}

func consenterChannelConfig(n *nwo.Network, o *nwo.Orderer) orderer.Consenter {
	host, port := conftx.OrdererClusterHostPort(n, o)
	tlsCert := parseCertificate(filepath.Join(n.OrdererLocalTLSDir(o), "server.crt"))
	return orderer.Consenter{
		Address: orderer.EtcdAddress{
			Host: host,
			Port: port,
		},
		ClientTLSCert: tlsCert,
		ServerTLSCert: tlsCert,
	}
}

type errorResponse struct {
	Error string `json:"error"`
}

func channelparticipationJoinFailure(n *nwo.Network, o *nwo.Orderer, channel string, block *common.Block, expectedStatus int, expectedError string) {
	blockBytes, err := proto.Marshal(block)
	Expect(err).NotTo(HaveOccurred())
	url := fmt.Sprintf("https://127.0.0.1:%d/participation/v1/channels", n.OrdererPort(o, nwo.OperationsPort))
	req := channelparticipation.GenerateJoinRequest(url, channel, blockBytes)
	authClient, _ := nwo.OrdererOperationalClients(n, o)

	doBodyFailure(authClient, req, expectedStatus, expectedError)
}

func doBodyFailure(client *http.Client, req *http.Request, expectedStatus int, expectedError string) {
	resp, err := client.Do(req)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(expectedStatus))
	body, err := ioutil.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	resp.Body.Close()

	errorResponse := &errorResponse{}
	err = json.Unmarshal(body, errorResponse)
	Expect(err).NotTo(HaveOccurred())
	Expect(errorResponse.Error).To(Equal(expectedError))
}

func channelparticipationRemoveFailure(n *nwo.Network, o *nwo.Orderer, channel string, expectedStatus int, expectedError string) {
	authClient, _ := nwo.OrdererOperationalClients(n, o)
	url := fmt.Sprintf("https://127.0.0.1:%d/participation/v1/channels/%s", n.OrdererPort(o, nwo.OperationsPort), channel)

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	Expect(err).NotTo(HaveOccurred())

	doBodyFailure(authClient, req, expectedStatus, expectedError)
}
