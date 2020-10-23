/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package participation

import (
	"fmt"
	"net/http"
)

// Lists the channels an OSN is a member of.
func ListAllChannels(osn, tlsDir string) (*http.Response, error) {
	url := fmt.Sprintf("https://%s/participation/v1/channels", osn)

	return httpGet(url, tlsDir)
}

// Lists a single channel an OSN is a member of.
func ListSingleChannel(osn, tlsDir, channelID string) (*http.Response, error) {
	url := fmt.Sprintf("https://%s/participation/v1/channels/%s", osn, channelID)

	return httpGet(url, tlsDir)
}
