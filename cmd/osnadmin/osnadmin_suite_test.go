/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main_test

import (
	"testing"

	cb "github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric/orderer/common/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gexec"
)

//go:generate counterfeiter -o mocks/channel_management.go -fake-name ChannelManagement . channelManagement

type channelManagement interface {
	ChannelList() types.ChannelList
	ChannelInfo(channelID string) (types.ChannelInfo, error)
	JoinChannel(channelID string, configBlock *cb.Block, isAppChannel bool) (types.ChannelInfo, error)
	RemoveChannel(channelID string) error
}

func TestOsnadmin(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "osnadmin Suite")
}

var cliPath string

var _ = BeforeSuite(func() {
	var err error
	cliPath, err = Build("github.com/hyperledger/fabric/cmd/osnadmin")
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	CleanupBuildArtifacts()
})
