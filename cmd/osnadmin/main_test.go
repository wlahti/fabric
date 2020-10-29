/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/golang/protobuf/proto"
	cb "github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/bccsp"
	"github.com/hyperledger/fabric/cmd/osnadmin/mocks"
	"github.com/hyperledger/fabric/common/crypto/tlsgen"
	"github.com/hyperledger/fabric/orderer/common/channelparticipation"
	"github.com/hyperledger/fabric/orderer/common/localconfig"
	"github.com/hyperledger/fabric/orderer/common/types"
	"github.com/hyperledger/fabric/protoutil"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	. "github.com/onsi/gomega/gexec"
)

var _ = Describe("osnadmin", func() {
	var (
		testServer *httptest.Server
		urlString  string
		tempDir    string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = ioutil.TempDir("", "opssys")
		Expect(err).NotTo(HaveOccurred())

		generateCertificates(tempDir)

		config := localconfig.ChannelParticipation{
			Enabled:            true,
			MaxRequestBodySize: 1024 * 1024,
		}
		mockChannelManagement := &mocks.ChannelManagement{}
		mockChannelManagement.ChannelListReturns(types.ChannelList{
			Channels: []types.ChannelInfoShort{
				{
					Name: "participation-trophy",
				},
				{
					Name: "another-participation-trophy",
				},
			},
		})
		mockChannelManagement.JoinChannelReturns(types.ChannelInfo{
			Name:            "apple",
			ClusterRelation: "banana",
			Status:          "orange",
			Height:          4,
		}, nil)

		h := channelparticipation.NewHTTPHandler(config, mockChannelManagement)
		Expect(h).NotTo(BeNil())
		testServer = httptest.NewUnstartedServer(h)

		cert, err := tls.LoadX509KeyPair(
			filepath.Join(tempDir, "server.crt"),
			filepath.Join(tempDir, "server.key"),
		)
		Expect(err).NotTo(HaveOccurred())
		caCertPool := x509.NewCertPool()
		caPem, err := ioutil.ReadFile(filepath.Join(tempDir, "ca.crt"))
		Expect(err).NotTo(HaveOccurred())
		caCertPool.AppendCertsFromPEM(caPem)

		testServer.TLS = &tls.Config{
			Certificates: []tls.Certificate{cert},
			ClientCAs:    caCertPool,
		}
		testServer.StartTLS()

		u, err := url.Parse(testServer.URL)
		Expect(err).NotTo(HaveOccurred())
		urlString = u.Host
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
		testServer.Close()
	})

	It("uses the channel participation API to list channels", func() {
		args := []string{"channel", "list", "--tlsDir", tempDir, "--orderer", urlString}
		process := runCommand(args)
		Eventually(process.Buffer()).Should(gbytes.Say("Status: 200"))
		expectedOutput := types.ChannelList{
			Channels: []types.ChannelInfoShort{
				{
					Name: "participation-trophy",
					URL:  "/participation/v1/channels/participation-trophy",
				},
				{
					Name: "another-participation-trophy",
					URL:  "/participation/v1/channels/another-participation-trophy",
				},
			},
		}
		json, err := json.MarshalIndent(expectedOutput, "", "\t")
		Expect(err).NotTo(HaveOccurred())
		Eventually(process.Buffer()).Should(gbytes.Say(fmt.Sprintf(`\Q%s\E`, string(json))))
	})

	It("uses the channel participation API to remove a channel", func() {
		args := []string{"channel", "remove", "--tlsDir", tempDir, "--orderer", urlString, "--channelID", "testing123"}
		process := runCommand(args)
		Eventually(process.Buffer()).Should(gbytes.Say("Status: 204"))
	})

	It("uses the channel participation API to join a channel", func() {
		configBlock := blockWithGroups(
			map[string]*cb.ConfigGroup{
				"Application": {},
			},
			"my-channel",
		)
		blockBytes, err := proto.Marshal(configBlock)
		Expect(err).NotTo(HaveOccurred())
		blockPath := filepath.Join(tempDir, "block.pb")
		err = ioutil.WriteFile(blockPath, blockBytes, 0644)
		Expect(err).NotTo(HaveOccurred())

		args := []string{"channel", "join", "--tlsDir", tempDir, "--orderer", urlString, "--channelID", "testing123", "--configBlock", blockPath}
		process := runCommand(args)
		Eventually(process.Buffer()).Should(gbytes.Say("Status: 201"))
		expectedOutput := types.ChannelInfo{
			Name:            "apple",
			URL:             "/participation/v1/channels/apple",
			ClusterRelation: "banana",
			Status:          "orange",
			Height:          4,
		}
		json, err := json.MarshalIndent(expectedOutput, "", "\t")
		Expect(err).NotTo(HaveOccurred())
		Eventually(process.Buffer()).Should(gbytes.Say(fmt.Sprintf(`\Q%s\E`, string(json))))
	})
})

func runCommand(args []string) *gexec.Session {
	cmd := exec.Command(cliPath, args...)
	process, err := Start(cmd, nil, nil)
	Expect(err).NotTo(HaveOccurred())
	Eventually(process).Should(Exit(0))
	return process
}

func generateCertificates(tempDir string) {
	serverCA, err := tlsgen.NewCA()
	Expect(err).NotTo(HaveOccurred())
	err = ioutil.WriteFile(filepath.Join(tempDir, "ca.crt"), serverCA.CertBytes(), 0640)
	Expect(err).NotTo(HaveOccurred())
	serverKeyPair, err := serverCA.NewServerCertKeyPair("127.0.0.1")
	Expect(err).NotTo(HaveOccurred())
	err = ioutil.WriteFile(filepath.Join(tempDir, "server.crt"), serverKeyPair.Cert, 0640)
	Expect(err).NotTo(HaveOccurred())
	err = ioutil.WriteFile(filepath.Join(tempDir, "server.key"), serverKeyPair.Key, 0640)
	Expect(err).NotTo(HaveOccurred())
}

func blockWithGroups(groups map[string]*cb.ConfigGroup, channelID string) *cb.Block {
	return &cb.Block{
		Data: &cb.BlockData{
			Data: [][]byte{
				protoutil.MarshalOrPanic(&cb.Envelope{
					Payload: protoutil.MarshalOrPanic(&cb.Payload{
						Data: protoutil.MarshalOrPanic(&cb.ConfigEnvelope{
							Config: &cb.Config{
								ChannelGroup: &cb.ConfigGroup{
									Groups: groups,
									Values: map[string]*cb.ConfigValue{
										"HashingAlgorithm": {
											Value: protoutil.MarshalOrPanic(&cb.HashingAlgorithm{
												Name: bccsp.SHA256,
											}),
										},
										"BlockDataHashingStructure": {
											Value: protoutil.MarshalOrPanic(&cb.BlockDataHashingStructure{
												Width: math.MaxUint32,
											}),
										},
										"OrdererAddresses": {
											Value: protoutil.MarshalOrPanic(&cb.OrdererAddresses{
												Addresses: []string{"localhost"},
											}),
										},
									},
								},
							},
						}),
						Header: &cb.Header{
							ChannelHeader: protoutil.MarshalOrPanic(&cb.ChannelHeader{
								Type:      int32(cb.HeaderType_CONFIG),
								ChannelId: channelID,
							}),
						},
					}),
				}),
			},
		},
	}
}
