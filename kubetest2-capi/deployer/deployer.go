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
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"time"

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
	provider            string
	kubernetesVersion   string
	controlPlaneCount   string
	workerCount         string
	flavor              string
	useExistingCluster  bool
	installCalico       bool
	kubecfgPath         string
	upTimeout           string
	workloadClusterName string
}

func (d *deployer) Kubeconfig() (string, error) {
	if d.kubecfgPath != "" {
		return d.kubecfgPath, nil
	}

	tmpdir, err := ioutil.TempDir("", "kubetest2-capi")
	if err != nil {
		return "", err
	}
	d.kubecfgPath = path.Join(tmpdir, "kubeconfig.yaml")
	args := []string{
		"get", "kubeconfig", d.workloadClusterName,
	}
	clusterctl := exec.Command("clusterctl", args...)
	lines, err := clusterctl.Output()
	if err != nil {
		return "", err
	}
	if err := ioutil.WriteFile(d.kubecfgPath, lines, 0600); err != nil {
		return "", err
	}

	return d.kubecfgPath, nil
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
	flags.StringVar(
		&d.flavor, "flavor", "", "--flavor flag for clusterctl",
	)
	flags.BoolVar(
		&d.useExistingCluster, "use-existing-cluster", false, "use the existing, currently targeted cluster as the management cluster",
	)
	flags.StringVar(
		&d.upTimeout, "up-timeout", "30m", "maximum time allotted for the --up command to complete",
	)
	flags.BoolVar(
		&d.installCalico, "install-calico", false, "automatically install the Calico CNI when the cluster becomes available",
	)
	flags.StringVar(
		&d.workloadClusterName, "workload-cluster-name", "capi-workload-cluster", "the workload cluster name",
	)
}

// assert that deployer implements types.DeployerWithKubeconfig
var _ types.DeployerWithKubeconfig = &deployer{}

// Deployer implementation methods below

func (d *deployer) Up() error {
	upTimeout, err := time.ParseDuration(d.upTimeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), upTimeout)
	defer cancel()

	if !d.useExistingCluster {
		if err := d.kind.Up(); err != nil {
			return err
		}
	}

	println("Up(): installing Cluster API...\n")
	args := []string{"get", "providers", "--all-namespaces", fmt.Sprintf("--field-selector=metadata.name=infrastructure-%s", d.provider), "--ignore-not-found"}
	kubectl := exec.CommandContext(ctx, "kubectl", args...)
	lines, err := kubectl.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if !bytes.Contains(exitErr.Stderr, []byte("the server doesn't have a resource type")) {
				return fmt.Errorf("error stdout: %q, stderr: %q, err: %w", string(lines), string(exitErr.Stderr), err)
			}
		} else {
			return err
		}
	}
	if len(lines) == 0 { // no results
		args = []string{"init", "--infrastructure", d.provider}
		if err := process.ExecJUnitContext(ctx, "clusterctl", args, os.Environ()); err != nil {
			return err
		}
	}

	println("waiting for CAPI to start")
	args = []string{"wait", "--for=condition=Available", "--all", "--all-namespaces", "deployment", "--timeout=-1m"}
	if err := process.ExecJUnitContext(ctx, "kubectl", args, os.Environ()); err != nil {
		return err
	}

	args = []string{
		"config",
		"cluster", d.workloadClusterName,
		"--infrastructure", d.provider,
		"--kubernetes-version", d.kubernetesVersion,
		"--worker-machine-count", d.workerCount,
		"--control-plane-machine-count", d.controlPlaneCount,
		"--flavor", d.flavor,
	}

	clusterctl := exec.CommandContext(ctx, "clusterctl", args...)
	clusterctl.Stderr = os.Stderr
	stdout, err := clusterctl.StdoutPipe()
	if err != nil {
		return err
	}

	kubectl = exec.CommandContext(ctx, "kubectl", "apply", "-f", "-")
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
	args = []string{"wait", "--for=condition=Ready", "cluster/" + d.workloadClusterName, "--timeout=-1m"}
	if err := process.ExecJUnitContext(ctx, "kubectl", args, os.Environ()); err != nil {
		return err
	}
	if d.installCalico {
		kubeconfig, err := d.Kubeconfig()
		if err != nil {
			return err
		}
		args = []string{"--kubeconfig", kubeconfig, "apply", "-f", "https://docs.projectcalico.org/v3.12/manifests/calico.yaml"}
		if err := process.ExecJUnitContext(ctx, "kubectl", args, os.Environ()); err != nil {
			return err
		}
		args = []string{"--kubeconfig", kubeconfig, "wait", "--for=condition=Available", "--all", "--all-namespaces", "deployment", "--timeout=-1m"}
		if err := process.ExecJUnitContext(ctx, "kubectl", args, os.Environ()); err != nil {
			return err
		}
	}

	return nil
}

func (d *deployer) Down() error {
	args := []string{"delete", "--ignore-not-found", "--wait", "cluster", d.kind.ClusterName}
	if err := process.ExecJUnit("kubectl", args, os.Environ()); err != nil {
		return err
	}

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