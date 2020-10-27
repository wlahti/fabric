/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import "io/ioutil"

type Config struct {
	OrdererEndpoint      string
	TlsDir               string
	ChannelID            string
	MarshaledConfigBlock []byte
}

func configFromFlags() (*Config, error) {
	c := &Config{
		OrdererEndpoint: *orderer,
		TlsDir:          *tlsDir,
		ChannelID:       *channelID,
	}

	if *configBlockPath != "" {
		blockBytes, err := ioutil.ReadFile(*configBlockPath)
		if err != nil {
			return nil, err
		}
		c.MarshaledConfigBlock = blockBytes
	}

	return c, nil
}
