/*
Copyright IBM Corp All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package nwo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"

	"github.com/gogo/protobuf/proto"
	"github.com/hyperledger/fabric-protos-go/common"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/onsi/gomega/types"
)

func ChannelParticipationJoin(n *Network, o *Orderer, channel string, block *common.Block, expectedChannelInfo ChannelInfo) {
	blockBytes, err := proto.Marshal(block)
	Expect(err).NotTo(HaveOccurred())
	url := fmt.Sprintf("https://127.0.0.1:%d/participation/v1/channels/%s", n.OrdererPort(o, OperationsPort), channel)
	req := generateJoinRequest(url, channel, blockBytes)
	authClient, _ := OrdererOperationalClients(n, o)

	By("joining channel " + expectedChannelInfo.Name)
	body := doBody(authClient, req)
	c := &ChannelInfo{}
	err = json.Unmarshal(body, c)
	Expect(err).NotTo(HaveOccurred())
	Expect(*c).To(Equal(expectedChannelInfo))
}

func generateJoinRequest(url, channel string, blockBytes []byte) *http.Request {
	joinBody := new(bytes.Buffer)
	writer := multipart.NewWriter(joinBody)
	part, err := writer.CreateFormFile("config-block", fmt.Sprintf("%s.block", channel))
	Expect(err).NotTo(HaveOccurred())
	part.Write(blockBytes)
	err = writer.Close()
	Expect(err).NotTo(HaveOccurred())

	req, err := http.NewRequest(http.MethodPost, url, joinBody)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return req
}

func doBody(client *http.Client, req *http.Request) []byte {
	resp, err := client.Do(req)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusCreated))
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	resp.Body.Close()

	return bodyBytes
}

type channelList struct {
	SystemChannel *channelInfoShort  `json:"systemChannel"`
	Channels      []channelInfoShort `json:"channels"`
}

type channelInfoShort struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func ChannelParticipationList(n *Network, o *Orderer, expectedChannels []string, systemChannel ...string) {
	authClient, unauthClient := OrdererOperationalClients(n, o)
	listChannelsURL := fmt.Sprintf("https://127.0.0.1:%d/participation/v1/channels", n.OrdererPort(o, OperationsPort))

	By("listing the channels")
	body := GetBody(authClient, listChannelsURL)()
	list := &channelList{}
	err := json.Unmarshal([]byte(body), list)
	Expect(err).NotTo(HaveOccurred())

	Expect(*list).To(MatchFields(IgnoreExtras, Fields{
		"Channels":      channelsMatcher(expectedChannels),
		"SystemChannel": systemChannelMatcher(systemChannel...),
	}))

	By("listing the channels without a client cert")
	resp, err := unauthClient.Get(listChannelsURL)
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))

	// for the current test scenarios, we only have these two cluster relations.
	// other possible values are "follower" and "config-tracker"
	// TODO generalize and relocate this after join operations are usable/testable
	clusterRelation := func(consensusType string) string {
		if consensusType == "etcdraft" {
			return "member"
		}
		return "none"
	}

	for _, channel := range list.Channels {
		expectedChannelInfo := ChannelInfo{
			Name:            channel.Name,
			URL:             channel.URL,
			Status:          "active",
			ClusterRelation: clusterRelation(n.Consensus.Type),
		}

		if len(n.PeersWithChannel(channel.Name)) > 0 {
			// get max peer height for the channel since there isn't a way to
			// directly get the orderer's height that I'm aware of beyond the channel
			// participation API (and maybe metrics?)
			maxPeerHeight := GetMaxLedgerHeight(n, channel.Name, n.PeersWithChannel(channel.Name)...)
			expectedChannelInfo.Height = uint64(maxPeerHeight)
		}

		ChannelParticipationListOne(n, o, expectedChannelInfo)
	}
}

func channelsMatcher(channels []string) types.GomegaMatcher {
	if len(channels) == 0 {
		return BeEmpty()
	}
	matchers := make([]types.GomegaMatcher, len(channels))
	for i, channel := range channels {
		matchers[i] = channelInfoShortMatcher(channel)
	}
	return ConsistOf(matchers)
}

func systemChannelMatcher(systemChannel ...string) types.GomegaMatcher {
	if len(systemChannel) == 0 {
		return BeNil()
	}
	return PointTo(channelInfoShortMatcher(systemChannel[0]))
}

func channelInfoShortMatcher(channel string) types.GomegaMatcher {
	return MatchFields(IgnoreExtras, Fields{
		"Name": Equal(channel),
		"URL":  Equal(fmt.Sprintf("/participation/v1/channels/%s", channel)),
	})
}

type ChannelInfo struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	Status          string `json:"status"`
	ClusterRelation string `json:"clusterRelation"`
	Height          uint64 `json:"height"`
}

func ChannelParticipationListOne(n *Network, o *Orderer, expectedChannelInfo ChannelInfo) {
	authClient, _ := OrdererOperationalClients(n, o)
	listChannelURL := fmt.Sprintf("https://127.0.0.1:%d/%s", n.OrdererPort(o, OperationsPort), expectedChannelInfo.URL)

	By("listing the details for channel " + expectedChannelInfo.Name)
	body := GetBody(authClient, listChannelURL)()
	c := &ChannelInfo{}
	err := json.Unmarshal([]byte(body), c)
	Expect(err).NotTo(HaveOccurred())
	expectedChannelInfo.URL = "" // list single channel always returns empty URL
	Expect(*c).To(Equal(expectedChannelInfo))
}
