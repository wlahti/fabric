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

func httpClient(caCertPool *x509.CertPool, tlsClientCert *tls.Certificate) *http.Client {
	tlsClientConfig := &tls.Config{
		RootCAs: caCertPool,
	}

	if tlsClientCert != nil {
		tlsClientConfig.Certificates = []tls.Certificate{*tlsClientCert}
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsClientConfig,
		},
	}
}

func httpDo(req *http.Request, caCertPool *x509.CertPool, tlsClientCert *tls.Certificate) (*http.Response, error) {
	client := httpClient(caCertPool, tlsClientCert)
	return client.Do(req)
}

func httpGet(url string, caCertPool *x509.CertPool, tlsClientCert *tls.Certificate) (*http.Response, error) {
	client := httpClient(caCertPool, tlsClientCert)
	return client.Get(url)
}
