/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package osnadmin

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net/http"
	"path/filepath"
)

func httpClient(tlsDir string) (*http.Client, error) {
	clientCert, err := tls.LoadX509KeyPair(
		filepath.Join(tlsDir, "server.crt"),
		filepath.Join(tlsDir, "server.key"),
	)
	if err != nil {
		return nil, err
	}

	clientCertPool := x509.NewCertPool()
	caCert, err := ioutil.ReadFile(filepath.Join(tlsDir, "ca.crt"))
	if err != nil {
		return nil, err
	}
	clientCertPool.AppendCertsFromPEM(caCert)

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{clientCert},
				RootCAs:      clientCertPool,
			},
		},
	}, nil
}

func httpDo(req *http.Request, tlsDir string) (*http.Response, error) {
	client, err := httpClient(tlsDir)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func httpGet(url, tlsDir string) (*http.Response, error) {
	client, err := httpClient(tlsDir)
	if err != nil {
		return nil, err
	}
	return client.Get(url)
}
