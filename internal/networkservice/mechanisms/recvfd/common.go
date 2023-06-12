// Copyright (c) 2020-2022 Cisco and/or its affiliates.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package recvfd

import (
	"context"
	"net/url"
	"sync"

	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/common"
	"github.com/kubeslice/cmd-forwarder-kernel/internal/tools/fs"
	"github.com/pkg/errors"
)

type perConnectionFileMapMap sync.Map

type perConnectionFileMap struct {
	filesByInodeURL    map[string]string
	inodeURLbyFilename map[string]*url.URL
}

func recvFDAndSwapInodeToFile(ctx context.Context, fileMap *perConnectionFileMap, parameters map[string]string) error {
	// Get the inodeURL from  parameters
	inodeURLStr, ok := parameters[common.InodeURL]
	if !ok {
		return nil
	}

	// Transform string to URL for correctness checking and ease of use
	inodeURL, err := url.Parse(inodeURLStr)
	if err != nil {
		return errors.WithStack(err)
	}

	// Is it an inode?
	if inodeURL.Scheme != "inode" {
		return nil
	}

	file, ok := fileMap.filesByInodeURL[inodeURLStr]
	if !ok {
		var err error
		file, err = fs.GetNetnsFilePath(inodeURLStr)
		if err != nil {
			return err
		}
		fileMap.filesByInodeURL[inodeURL.String()] = file
	}
        // Swap out the inodeURL for a fileURL in the parameters
	fileURL := &url.URL{Scheme: "file", Path: file}
	parameters[common.InodeURL] = fileURL.String()

	// Remember the swap so we can undo it later
	fileMap.inodeURLbyFilename[file] = inodeURL

	return err
}

func swapFileToInode(fileMap *perConnectionFileMap, parameters map[string]string) error {
	// Get the inodeURL from  parameters
	fileURLStr, ok := parameters[common.InodeURL]
	if !ok {
		return nil
	}

	// Transform string to URL for correctness checking and ease of use
	fileURL, err := url.Parse(fileURLStr)
	if err != nil {
		return errors.WithStack(err)
	}

	// Is it a file?
	if fileURL.Scheme != "file" {
		return nil
	}

        // Do we have an inodeURL to translate it back to?
		inodeURL, ok := fileMap.inodeURLbyFilename[fileURL.Path]
		if !ok {
			return nil
		}
		// Swap the fileURL for the inodeURL in parameters
		parameters[common.InodeURL] = inodeURL.String()

		// This is used to clean up files sent by MechanismPreferences that were *not* selected to be the
		// connection mechanism
		for inodeURLStr, _ := range fileMap.filesByInodeURL {
			if inodeURLStr != inodeURL.String() {
				delete(fileMap.filesByInodeURL, inodeURLStr)
			}
		}
	return nil
}

