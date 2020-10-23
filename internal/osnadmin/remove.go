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

// Removes an OSN from an existing channel.
func Remove(osn, channelID string, tlsClientCert tls.Certificate, tlsCACert *x509.Certificate) (*http.Response, error) {
	url := fmt.Sprintf("https://%s/participation/v1/channels/%s", osn, channelID)

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}

	return httpDo(req, tlsClientCert, tlsCACert)
}
