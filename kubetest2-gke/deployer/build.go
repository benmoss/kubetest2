/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package deployer

import (
	"fmt"
	"strings"
)

func (d *deployer) Build() error {
	if err := d.verifyBuildFlags(); err != nil {
		return err
	}
	version, err := d.BuildOptions.Build()
	if err != nil {
		return err
	}
	version = strings.TrimPrefix(version, "v")
	version += ".0+" + d.commonOptions.RunID()
	if d.BuildOptions.StageLocation != "" {
		if err := d.BuildOptions.Stage(version); err != nil {
			return fmt.Errorf("error staging build: %v", err)
		}
	}
	d.Version = version
	return nil
}

func (d *deployer) verifyBuildFlags() error {
	if d.RepoRoot == "" {
		return fmt.Errorf("required repo-root when building from source")
	}
	d.BuildOptions.RepoRoot = d.RepoRoot
	return d.BuildOptions.Validate()
}
