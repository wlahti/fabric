/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package osnadmin

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
)

func httpClient(tlsClientCert tls.Certificate, tlsCACert *x509.Certificate) *http.Client {
	clientCertPool := x509.NewCertPool()
	clientCertPool.AddCert(tlsCACert)

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{tlsClientCert},
				RootCAs:      clientCertPool,
			},
		},
	}
}

func httpDo(req *http.Request, tlsClientCert tls.Certificate, tlsCACert *x509.Certificate) (*http.Response, error) {
	client := httpClient(tlsClientCert, tlsCACert)
	return client.Do(req)
}

func httpGet(url string, tlsClientCert tls.Certificate, tlsCACert *x509.Certificate) (*http.Response, error) {
	client := httpClient(tlsClientCert, tlsCACert)
	return client.Get(url)
}
