/*
Copyright 2019 The Kubernetes Authors.

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

// Package deployer implements the kubetest2 kind deployer
package deployer

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/pflag"

	kinddeployer "sigs.k8s.io/kubetest2/kubetest2-kind/deployer"
	"sigs.k8s.io/kubetest2/pkg/process"
	"sigs.k8s.io/kubetest2/pkg/types"
)

// Name is the name of the deployer
const Name = "capi"

// New implements deployer.New for capi
func New(opts types.Options) (types.Deployer, *pflag.FlagSet) {
	// create a deployer object and set fields that are not flag controlled
	kind, flags := kinddeployer.New(opts)
	d := &deployer{
		kind:          kind.(*kinddeployer.Deployer),
		commonOptions: opts,
	}
	// register flags and return
	bindFlags(d, flags)
	return d, flags
}

// assert that New implements types.NewDeployer
var _ types.NewDeployer = New

type deployer struct {
	// generic parts
	commonOptions types.Options
	kind          *kinddeployer.Deployer
	// capi specific details
	provider          string
	kubernetesVersion string
	controlPlaneCount string
	workerCount       string
}

func (d *deployer) Kubeconfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kube", "config"), nil
}

// helper used to create & bind a flagset to the deployer
func bindFlags(d *deployer, flags *pflag.FlagSet) {
	flags.StringVar(
		&d.provider, "provider", "", "--provider flag for clusterctl",
	)
	flags.StringVar(
		&d.kubernetesVersion, "kubernetes-version", "", "--kubernetes-version flag for clusterctl",
	)
	flags.StringVar(
		&d.controlPlaneCount, "control-plane-machine-count", "1", "--control-plane-machine-count flag for clusterctl",
	)
	flags.StringVar(
		&d.workerCount, "worker-machine-count", "1", "--worker-machine-count flag for clusterctl",
	)
}

// assert that deployer implements types.DeployerWithKubeconfig
var _ types.DeployerWithKubeconfig = &deployer{}

// Deployer implementation methods below

func (d *deployer) Up() error {
	if err := d.kind.Up(); err != nil {
		return err
	}

	println("Up(): installing Cluster API...\n")
	args := []string{"init", "--infrastructure", d.provider}
	if err := process.ExecJUnit("clusterctl", args, os.Environ()); err != nil {
		return err
	}
	println("waiting for CAPI to start")
	args = []string{"-n", "capi-system", "wait", "--for=condition=Available", "deployment/capi-controller-manager", "--timeout=10m"}
	if err := process.ExecJUnit("kubectl", args, os.Environ()); err != nil {
		return err
	}
	args = []string{"-n", "capi-webhook-system", "wait", "--for=condition=Available", "deployment/capi-controller-manager", "--timeout=10m"}
	if err := process.ExecJUnit("kubectl", args, os.Environ()); err != nil {
		return err
	}

	args = []string{"config", "cluster", d.kind.ClusterName,
		"--infrastructure", d.provider,
		"--kubernetes-version", d.kubernetesVersion,
		"--worker-machine-count", d.workerCount,
		"--control-plane-machine-count", d.controlPlaneCount,
	}

	clusterctl := exec.Command("clusterctl", args...)
	clusterctl.Stderr = os.Stderr
	stdout, err := clusterctl.StdoutPipe()
	if err != nil {
		return err
	}

	kubectl := exec.Command("kubectl", "apply", "-f", "-")
	kubectl.Stdin = stdout
	kubectl.Stdout = os.Stdout
	kubectl.Stderr = os.Stderr

	if err := clusterctl.Start(); err != nil {
		return err
	}
	if err := kubectl.Start(); err != nil {
		return err
	}
	if err := clusterctl.Wait(); err != nil {
		return err
	}
	if err := kubectl.Wait(); err != nil {
		return err
	}

	println("waiting for cluster to become ready")
	args = []string{"wait", "--for=condition=Ready", "cluster/" + d.kind.ClusterName, "--timeout=30m"}
	if err := process.ExecJUnit("kubectl", args, os.Environ()); err != nil {
		return err
	}

	return nil
}

func (d *deployer) Down() error {
	return d.kind.Down()
}

func (d *deployer) IsUp() (up bool, err error) {
	return d.kind.IsUp()
}

func (d *deployer) DumpClusterLogs() error {
	return d.kind.DumpClusterLogs()
}

func (d *deployer) Build() error {
	return d.kind.Build()
}
