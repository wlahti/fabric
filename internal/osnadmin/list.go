/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package osnadmin

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
)

// Lists the channels an OSN is a member of.
func ListAllChannels(osn string, tlsClientCert tls.Certificate, tlsCACert *x509.Certificate) (*http.Response, error) {
	url := fmt.Sprintf("https://%s/participation/v1/channels", osn)

	return httpGet(url, tlsClientCert, tlsCACert)
}

// Lists a single channel an OSN is a member of.
func ListSingleChannel(osn, channelID string, tlsClientCert tls.Certificate, tlsCACert *x509.Certificate) (*http.Response, error) {
	url := fmt.Sprintf("https://%s/participation/v1/channels/%s", osn, channelID)

	return httpGet(url, tlsClientCert, tlsCACert)
}
