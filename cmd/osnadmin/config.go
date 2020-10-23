/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
)

type Config struct {
	OrdererEndpoint      string
	TlsClientCert        tls.Certificate
	TlsCACert            *x509.Certificate
	ChannelID            string
	MarshaledConfigBlock []byte
}

func configFromFlags() (*Config, error) {
	clientCert, err := tls.LoadX509KeyPair(
		filepath.Join(*tlsDir, "server.crt"),
		filepath.Join(*tlsDir, "server.key"),
	)
	if err != nil {
		return nil, fmt.Errorf("loading server cert/key pair: %s", err)
	}

	caCertPEM, err := ioutil.ReadFile(filepath.Join(*tlsDir, "ca.crt"))
	if err != nil {
		return nil, fmt.Errorf("reading ca.crt: %s", err)
	}

	caCert, err := decodePEMCertificate(caCertPEM)
	if err != nil {
		return nil, fmt.Errorf("decoding ca.crt PEM: %s", err)
	}

	c := &Config{
		OrdererEndpoint: *orderer,
		TlsClientCert:   clientCert,
		TlsCACert:       caCert,
		ChannelID:       *channelID,
	}

	if *configBlockPath != "" {
		blockBytes, err := ioutil.ReadFile(*configBlockPath)
		if err != nil {
			return nil, fmt.Errorf("reading config block: %s", err)
		}
		c.MarshaledConfigBlock = blockBytes
	}

	return c, nil
}

func decodePEMCertificate(pemCert []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemCert)
	if block == nil {
		return nil, errors.New("empty block")
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("expected CERTIFICATE got %s", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	return cert, nil
}
