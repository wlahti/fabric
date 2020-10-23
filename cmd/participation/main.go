/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/hyperledger/fabric/internal/participation"
)

func main() {
	var osn, channelID, tlsDir, configBlockPath string

	listCmd := flag.NewFlagSet("list", flag.ExitOnError)
	listCmd.StringVar(&osn, "orderer", "", "Ordering service endpoint")
	listCmd.StringVar(&tlsDir, "tlsDir", "", "Path to directory containing TLS materials")
	listCmd.StringVar(&channelID, "channelID", "", "Channel ID")

	removeCmd := flag.NewFlagSet("remove", flag.ExitOnError)
	removeCmd.StringVar(&osn, "orderer", "", "Ordering service endpoint")
	removeCmd.StringVar(&channelID, "channelID", "", "Channel ID")
	removeCmd.StringVar(&tlsDir, "tlsDir", "", "Path to directory containing TLS materials")

	joinCmd := flag.NewFlagSet("join", flag.ExitOnError)
	joinCmd.StringVar(&osn, "orderer", "", "Ordering service endpoint")
	joinCmd.StringVar(&tlsDir, "tlsDir", "", "Path to directory containing TLS materials")
	joinCmd.StringVar(&channelID, "channelID", "", "Channel ID")
	joinCmd.StringVar(&configBlockPath, "configBlock", "", "Path to file containing config block")

	var (
		resp *http.Response
		err  error
	)

	switch os.Args[1] {
	case "list":
		listCmd.Parse(os.Args[2:])
		if listCmd.NFlag() >= 2 {
			if channelID != "" {
				resp, err = participation.ListSingleChannel(osn, tlsDir, channelID)
				break
			}
			resp, err = participation.ListAllChannels(osn, tlsDir)
		}
	case "remove":
		removeCmd.Parse(os.Args[2:])
		if removeCmd.NFlag() >= 3 {
			resp, err = participation.Remove(osn, tlsDir, channelID)
		}
	case "join":
		joinCmd.Parse(os.Args[2:])
		if joinCmd.NFlag() >= 3 {
			resp, err = participation.Join(osn, tlsDir, channelID, configBlockPath)
		}
	default:
		flag.Parse()
	}

	if err != nil {
		fmt.Printf("Error: %s\n", err)
	}

	printResponse(resp, os.Stdout)
}

func printResponse(resp *http.Response, out io.Writer) {
	bodyBytes, err := readBodyBytes(resp.Body)
	if err != nil {
		log.Fatalf("failed to read http response body: %s", err)
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
