/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/hyperledger/fabric/internal/osnadmin"
	"gopkg.in/alecthomas/kingpin.v2"
)

// command line flags
var (
	app       = kingpin.New("osnadmin", "Orderer Service Node (OSN) administration")
	orderer   = app.Flag("orderer", "Endpoint of the OSN").String()
	tlsDir    = app.Flag("tlsDir", "Path to the directory containing the TLS server.crt, server.key, and ca.crt").String()
	channelID = app.Flag("channelID", "Channel ID - removed for join now?").String()

	channel = app.Command("channel", "Channel actions")

	join            = channel.Command("join", "Join an Ordering Service Node (OSN) to a channel. If the channel does not yet exist, it will be created.")
	configBlockPath = join.Flag("configBlock", "Path to the file containing the config block").String()

	list = channel.Command("list", "List channel information about the Ordering Service Node (OSN). If the channelID flag is set, more detailed information will be provided for that channel.")

	remove = channel.Command("remove", "Remove an Ordering Service Node (OSN) from a channel.")
)

func main() {
	kingpin.Version("0.0.1")
	command := kingpin.MustParse(app.Parse(os.Args[1:]))
	config, err := configFromFlags()
	if err != nil {
		printErrorAndExit(err)
	}

	var resp *http.Response

	switch command {
	case join.FullCommand():
		resp, err = osnadmin.Join(config.OrdererEndpoint, config.ChannelID, config.MarshaledConfigBlock, config.TlsClientCert, config.TlsCACert)
	case list.FullCommand():
		if config.ChannelID != "" {
			resp, err = osnadmin.ListSingleChannel(config.OrdererEndpoint, config.ChannelID, config.TlsClientCert, config.TlsCACert)
			break
		}
		resp, err = osnadmin.ListAllChannels(config.OrdererEndpoint, config.TlsClientCert, config.TlsCACert)
	case remove.FullCommand():
		resp, err = osnadmin.Remove(config.OrdererEndpoint, config.ChannelID, config.TlsClientCert, config.TlsCACert)
	}

	if err != nil {
		printErrorAndExit(err)
	}

	printResponse(resp, os.Stdout)
}

func printResponse(resp *http.Response, out io.Writer) {
	bodyBytes, err := readBodyBytes(resp.Body)
	if err != nil {
		printErrorAndExit(err)
	}
	var buffer bytes.Buffer
	fmt.Printf("Status: %d\n", resp.StatusCode)
	json.Indent(&buffer, bodyBytes, "", "\t")
	buffer.WriteTo(out)
}

func readBodyBytes(body io.ReadCloser) ([]byte, error) {
	bodyBytes, err := ioutil.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("reading http response body: %s", err)
	}
	body.Close()

	return bodyBytes, nil
}

func printErrorAndExit(err error) {
	fmt.Printf("Error: %s\n", err)
	os.Exit(1)
}
