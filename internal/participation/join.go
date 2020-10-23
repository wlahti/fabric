/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package participation

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
)

// Joins an OSN to a new or existing channel.
func Join(osn, tlsDir, channelID, configBlockPath string) (*http.Response, error) {
	blockBytes, err := ioutil.ReadFile(configBlockPath)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s/participation/v1/channels", osn)
	req, err := createJoinRequest(url, channelID, blockBytes)
	if err != nil {
		return nil, err
	}

	return httpDo(req, tlsDir)
}

func createJoinRequest(url, channelID string, blockBytes []byte) (*http.Request, error) {
	joinBody := new(bytes.Buffer)
	writer := multipart.NewWriter(joinBody)
	part, err := writer.CreateFormFile("config-block", fmt.Sprintf("%s.block", channelID))
	if err != nil {
		return nil, err
	}
	part.Write(blockBytes)
	err = writer.Close()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, joinBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return req, nil
}
