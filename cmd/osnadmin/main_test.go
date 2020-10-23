/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
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
)

var _ = Describe("osnadmin", func() {
	var (
		tempDir               string
		mockChannelManagement *mocks.ChannelManagement
		testServer            *httptest.Server
		urlString             string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = ioutil.TempDir("", "osnadmin")
		Expect(err).NotTo(HaveOccurred())

		generateCertificates(tempDir)

		config := localconfig.ChannelParticipation{
			Enabled:            true,
			MaxRequestBodySize: 1024 * 1024,
		}
		mockChannelManagement = &mocks.ChannelManagement{}

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

	Describe("List", func() {
		BeforeEach(func() {
			mockChannelManagement.ChannelListReturns(types.ChannelList{
				Channels: []types.ChannelInfoShort{
					{
						Name: "participation-trophy",
					},
					{
						Name: "another-participation-trophy",
					},
				},
				SystemChannel: &types.ChannelInfoShort{
					Name: "fight-the-system",
				},
			})

			mockChannelManagement.ChannelInfoReturns(types.ChannelInfo{
				Name:            "asparagus",
				ClusterRelation: "broccoli",
				Status:          "carrot",
				Height:          987,
			}, nil)
		})

		It("uses the channel participation API to list all application and and the system channel (when it exists)", func() {
			args := []string{
				"channel",
				"list",
				"--tlsDir", tempDir,
				"--orderer", urlString,
			}
			sess := runCommand(args, 0)

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
				SystemChannel: &types.ChannelInfoShort{
					Name: "fight-the-system",
					URL:  "/participation/v1/channels/fight-the-system",
				},
			}
			checkOutput(sess, 200, expectedOutput)
		})

		It("uses the channel participation API to list the details of a single channel", func() {
			args := []string{
				"channel",
				"list",
				"--tlsDir", tempDir,
				"--orderer", urlString,
				"--channelID", "tell-me-your-secrets",
			}
			sess := runCommand(args, 0)

			expectedOutput := types.ChannelInfo{
				Name:            "asparagus",
				URL:             "/participation/v1/channels/asparagus",
				ClusterRelation: "broccoli",
				Status:          "carrot",
				Height:          987,
			}
			checkOutput(sess, 200, expectedOutput)
		})

		Context("when the channel does not exist", func() {
			BeforeEach(func() {
				mockChannelManagement.ChannelInfoReturns(types.ChannelInfo{}, errors.New("eat-your-peas"))
			})

			It("returns 404 not found", func() {
				args := []string{
					"channel",
					"list",
					"--tlsDir", tempDir,
					"--orderer", urlString,
					"--channelID", "tell-me-your-secrets",
				}
				sess := runCommand(args, 0)

				expectedOutput := types.ErrorResponse{
					Error: "eat-your-peas",
				}
				checkOutput(sess, 404, expectedOutput)
			})
		})
	})

	Describe("Remove", func() {
		It("uses the channel participation API to remove a channel", func() {
			args := []string{
				"channel",
				"remove",
				"--tlsDir", tempDir,
				"--orderer", urlString,
				"--channelID", "testing123",
			}
			sess := runCommand(args, 0)

			Eventually(sess.Buffer()).Should(gbytes.Say("Status: 204"))
		})

		Context("when the channel does not exist", func() {
			BeforeEach(func() {
				mockChannelManagement.RemoveChannelReturns(types.ErrChannelNotExist)
			})

			It("returns 404 not found", func() {
				args := []string{
					"channel",
					"remove",
					"--tlsDir", tempDir,
					"--orderer", urlString,
					"--channelID", "tell-me-your-secrets",
				}
				sess := runCommand(args, 0)

				expectedOutput := types.ErrorResponse{
					Error: "cannot remove: channel does not exist",
				}
				checkOutput(sess, 404, expectedOutput)
			})
		})
	})

	Describe("Join", func() {
		var blockPath string

		BeforeEach(func() {
			configBlock := blockWithGroups(
				map[string]*cb.ConfigGroup{
					"Application": {},
				},
				"my-channel",
			)
			blockPath = createBlockFile(tempDir, configBlock)

			mockChannelManagement.JoinChannelReturns(types.ChannelInfo{
				Name:            "apple",
				ClusterRelation: "banana",
				Status:          "orange",
				Height:          123,
			}, nil)
		})

		It("uses the channel participation API to join a channel", func() {
			args := []string{
				"channel",
				"join",
				"--tlsDir", tempDir,
				"--orderer", urlString,
				"--channelID", "testing123",
				"--configBlock", blockPath,
			}
			sess := runCommand(args, 0)

			expectedOutput := types.ChannelInfo{
				Name:            "apple",
				URL:             "/participation/v1/channels/apple",
				ClusterRelation: "banana",
				Status:          "orange",
				Height:          123,
			}
			checkOutput(sess, 201, expectedOutput)
		})

		Context("when the block isn't a valid config block", func() {
			BeforeEach(func() {
				blockPath = createBlockFile(tempDir, &cb.Block{})
			})

			It("returns 405 bad request", func() {
				args := []string{
					"channel",
					"join",
					"--tlsDir", tempDir,
					"--orderer", urlString,
					"--channelID", "testing123",
					"--configBlock", blockPath,
				}
				sess := runCommand(args, 0)

				expectedOutput := types.ErrorResponse{
					Error: "invalid join block: block is not a config block",
				}
				checkOutput(sess, 400, expectedOutput)
			})
		})

		Context("when joining the channel fails", func() {
			BeforeEach(func() {
				mockChannelManagement.JoinChannelReturns(types.ChannelInfo{}, types.ErrChannelAlreadyExists)
			})

			It("returns 405 not allowed", func() {
				args := []string{
					"channel",
					"join",
					"--tlsDir", tempDir,
					"--orderer", urlString,
					"--channelID", "testing123",
					"--configBlock", blockPath,
				}
				sess := runCommand(args, 0)

				expectedOutput := types.ErrorResponse{
					Error: "cannot join: channel already exists",
				}
				checkOutput(sess, 405, expectedOutput)
			})
		})

		Describe("Flags", func() {
			Context("when the server cert/key pair fail to load", func() {
				BeforeEach(func() {
					tempDir = "not-the-directory-youre-looking-for"
				})

				It("returns with exit code 1 and prints the error", func() {
					args := []string{
						"channel",
						"join",
						"--tlsDir", tempDir,
					}
					sess := runCommand(args, 1)

					checkCLIError(sess, "loading server cert/key pair: open not-the-directory-youre-looking-for/server.crt: no such file or directory")
				})
			})

			Context("when the ca cert cannot be read", func() {
				BeforeEach(func() {
					err := os.Remove(filepath.Join(tempDir, "ca.crt"))
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns with exit code 1 and prints the error", func() {
					args := []string{
						"channel",
						"list",
						"--tlsDir", tempDir,
					}
					sess := runCommand(args, 1)

					checkCLIError(sess, fmt.Sprintf("reading ca.crt: open %s: no such file or directory", filepath.Join(tempDir, "ca.crt")))
				})
			})

			Context("when the ca cert is an empty file", func() {
				BeforeEach(func() {
					err := ioutil.WriteFile(filepath.Join(tempDir, "ca.crt"), []byte{}, 0644)
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns with exit code 1 and prints the error", func() {
					args := []string{
						"channel",
						"remove",
						"--tlsDir", tempDir,
					}
					sess := runCommand(args, 1)

					checkCLIError(sess, "decoding ca.crt PEM: empty block")
				})
			})

			Context("when the ca cert is a not a certificate", func() {
				BeforeEach(func() {
					keyBytes, err := ioutil.ReadFile(filepath.Join(tempDir, "server.key"))
					Expect(err).NotTo(HaveOccurred())
					err = ioutil.WriteFile(filepath.Join(tempDir, "ca.crt"), keyBytes, 0644)
					Expect(err).NotTo(HaveOccurred())
				})

				It("returns with exit code 1 and prints the error", func() {
					args := []string{
						"channel",
						"remove",
						"--tlsDir", tempDir,
					}
					sess := runCommand(args, 1)

					checkCLIError(sess, "decoding ca.crt PEM: expected CERTIFICATE got EC PRIVATE KEY")
				})
			})

			Context("when the config block cannot be read", func() {
				var configBlockPath string

				BeforeEach(func() {
					configBlockPath = "not-the-config-block-youre-looking-for"
				})

				It("returns with exit code 1 and prints the error", func() {
					args := []string{
						"channel",
						"join",
						"--tlsDir", tempDir,
						"--configBlock", configBlockPath,
					}
					sess := runCommand(args, 1)

					checkCLIError(sess, "reading config block: open not-the-config-block-youre-looking-for: no such file or directory")
				})
			})
		})
	})
})

func runCommand(args []string, expectedExitCode int) *gexec.Session {
	cmd := exec.Command(cliPath, args...)
	sess, err := gexec.Start(cmd, nil, nil)
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess).Should(gexec.Exit(expectedExitCode))
	return sess
}

func checkOutput(sess *gexec.Session, expectedStatus int, expectedOutput interface{}) {
	Eventually(sess.Buffer()).Should(gbytes.Say(fmt.Sprintf("Status: %d", expectedStatus)))
	json, err := json.MarshalIndent(expectedOutput, "", "\t")
	Expect(err).NotTo(HaveOccurred())
	Eventually(sess.Buffer()).Should(gbytes.Say(fmt.Sprintf(`\Q%s\E`, string(json))))
}

func checkCLIError(sess *gexec.Session, expectedError string) {
	Eventually(sess.Buffer()).Should(gbytes.Say("Error: " + expectedError))

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

func createBlockFile(tempDir string, configBlock *cb.Block) string {
	blockBytes, err := proto.Marshal(configBlock)
	Expect(err).NotTo(HaveOccurred())
	blockPath := filepath.Join(tempDir, "block.pb")
	err = ioutil.WriteFile(blockPath, blockBytes, 0644)
	Expect(err).NotTo(HaveOccurred())
	return blockPath
}
