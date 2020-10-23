/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hyperledger/fabric/common/crypto/tlsgen"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	. "github.com/onsi/gomega/gexec"
)

func TestChannelFlags(t *testing.T) {
	gt := NewGomegaWithT(t)
	cli, err := Build("github.com/hyperledger/fabric/cmd/osnadmin")
	gt.Expect(err).NotTo(HaveOccurred())
	defer CleanupBuildArtifacts()

	tempDir, err := ioutil.TempDir("", "osnadmin")
	gt.Expect(err).NotTo(HaveOccurred())
	generateCertificates(t, tempDir)

	tests := []struct {
		name        string
		flags       []string
		expectedErr string
	}{
		{
			name:        "join with bad config block",
			flags:       []string{"join", "--configBlock", "not-a-config-block", "--orderer", "fake-orderer", "--channelID", "fake-channel", "--tlsDir", tempDir},
			expectedErr: "Error: reading config block: open not-a-config-block: no such file or directory",
		},
		{
			name:        "list with non-existent tlsDir",
			flags:       []string{"list", "--tlsDir", "not-a-tls-dir", "--orderer", "fake-orderer", "--channelID", "fake-channel"},
			expectedErr: "Error: loading server cert/key pair: open not-a-tls-dir/server.crt: no such file or directory",
		},
		{
			name:        "remove with non-existent tlsDir",
			flags:       []string{"remove", "--tlsDir", "not-a-tls-dir", "--orderer", "fake-orderer", "--channelID", "fake-channel"},
			expectedErr: "Error: loading server cert/key pair: open not-a-tls-dir/server.crt: no such file or directory",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gt := NewGomegaWithT(t)

			args := append([]string{"channel"}, test.flags...)
			cmd := exec.Command(cli, args...)
			process, err := Start(cmd, nil, nil)
			gt.Expect(err).NotTo(HaveOccurred())
			gt.Eventually(process).Should(Exit(1))
			gt.Expect(process.Buffer()).To(gbytes.Say(test.expectedErr))
		})
	}
}

func generateCertificates(t *testing.T, tempDir string) {
	gt := NewGomegaWithT(t)
	serverCA, err := tlsgen.NewCA()
	gt.Expect(err).NotTo(HaveOccurred())
	err = ioutil.WriteFile(filepath.Join(tempDir, "ca.crt"), serverCA.CertBytes(), 0640)
	gt.Expect(err).NotTo(HaveOccurred())
	serverKeyPair, err := serverCA.NewServerCertKeyPair("127.0.0.1")
	gt.Expect(err).NotTo(HaveOccurred())
	err = ioutil.WriteFile(filepath.Join(tempDir, "server.crt"), serverKeyPair.Cert, 0640)
	gt.Expect(err).NotTo(HaveOccurred())
	err = ioutil.WriteFile(filepath.Join(tempDir, "server.key"), serverKeyPair.Key, 0640)
	gt.Expect(err).NotTo(HaveOccurred())
}
