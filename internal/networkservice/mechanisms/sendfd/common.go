/*
 *  Copyright (c) 2023 Avesha, Inc. All rights reserved.
 *
 *  SPDX-License-Identifier: Apache-2.0
 *
 *  Licensed under the Apache License, Version 2.0 (the "License");
 *  you may not use this file except in compliance with the License.
 *  You may obtain a copy of the License at
 *
 *  http://www.apache.org/licenses/LICENSE-2.0
 *
 *  Unless required by applicable law or agreed to in writing, software
 *  distributed under the License is distributed on an "AS IS" BASIS,
 *  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *  See the License for the specific language governing permissions and
 *  limitations under the License.
 */

package sendfd

import (
	"net/url"

	"github.com/kubeslice/cmd-forwarder-kernel/internal/tools/fs"
	"github.com/networkservicemesh/api/pkg/api/networkservice/mechanisms/common"
	"github.com/pkg/errors"
)

func swapFileToInode(parameters, inodeURLToFileURLMap map[string]string) error {
	// Does it have parameters?
	if parameters == nil {
		return nil
	}
	// Does it have the InodeURL Parameter?
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

	inodeURL, err := fs.GetNetnsInodeFromFile(fileURLStr)
	if err != nil {
		return errors.WithStack(err)
	}
	// swap the InodeURL parameter for a real inode://${dev}/${ino} URL
	parameters[common.InodeURL] = inodeURL.String()
	// remember the original fileURL so we can translate back later
	inodeURLToFileURLMap[inodeURL.String()] = fileURLStr
	return nil
}
