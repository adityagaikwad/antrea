// Copyright 2020 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func skipIfProxyDisabled(t *testing.T, data *TestData) {
	if enabled, err := proxyEnabled(data); err != nil {
		t.Fatalf("Error when detecting proxy: %v", err)
	} else if !enabled {
		t.Skip()
	}
}

func proxyEnabled(data *TestData) (bool, error) {
	key := "resubmit(,40),resubmit(,41)"
	agentName, err := data.getAntreaPodOnNode(masterNodeName())
	if err != nil {
		return false, err
	}
	table31Output, _, err := data.runCommandFromPod(metav1.NamespaceSystem, agentName, "antrea-agent", []string{"ovs-ofctl", "dump-flows", defaultBridgeName, "table=31"})
	return strings.Contains(table31Output, key), err
}

func TestProxyServiceSessionAffinity(t *testing.T) {
	skipIfProviderIs(t, "kind", "#881 Does not work in Kind, needs to be investigated.")
	data, err := setupTest(t)
	if err != nil {
		t.Fatalf("Error when setting up test: %v", err)
	}
	defer teardownTest(t, data)

	skipIfProxyDisabled(t, data)

	nodeName := nodeName(1)
	require.NoError(t, data.createNginxPod("nginx", nodeName))
	nginxIP, err := data.podWaitForIP(defaultTimeout, "nginx", testNamespace)
	require.NoError(t, err)
	require.NoError(t, data.podWaitForRunning(defaultTimeout, "nginx", testNamespace))
	svc, err := data.createNginxService(true)
	require.NoError(t, err)
	require.NoError(t, data.createBusyboxPodOnNode("busybox", nodeName))
	require.NoError(t, data.podWaitForRunning(defaultTimeout, "busybox", testNamespace))
	stdout, stderr, err := data.runCommandFromPod(testNamespace, "busybox", busyboxContainerName, []string{"wget", "-O", "-", svc.Spec.ClusterIP, "-T", "1"})
	require.NoError(t, err, fmt.Sprintf("stdout: %s\n, stderr: %s", stdout, stderr))
	agentName, err := data.getAntreaPodOnNode(nodeName)
	require.NoError(t, err)
	table40Output, _, err := data.runCommandFromPod(metav1.NamespaceSystem, agentName, "antrea-agent", []string{"ovs-ofctl", "dump-flows", defaultBridgeName, "table=40"})
	require.NoError(t, err)
	require.Contains(t, table40Output, fmt.Sprintf("nw_dst=%s,tp_dst=80", svc.Spec.ClusterIP))
	require.Contains(t, table40Output, fmt.Sprintf("load:0x%s->NXM_NX_REG3[]", strings.TrimLeft(hex.EncodeToString(net.ParseIP(nginxIP).To4()), "0")))
}

func TestProxyHairpin(t *testing.T) {
	data, err := setupTest(t)
	if err != nil {
		t.Fatalf("Error when setting up test: %v", err)
	}
	defer teardownTest(t, data)

	skipIfProxyDisabled(t, data)

	nodeName := nodeName(1)
	err = data.createPodOnNode("busybox", nodeName, "busybox", []string{"nc", "-lk", "-p", "80"}, nil, nil, []v1.ContainerPort{{ContainerPort: 80, Protocol: v1.ProtocolTCP}})
	require.NoError(t, err)
	require.NoError(t, data.podWaitForRunning(defaultTimeout, "busybox", testNamespace))
	svc, err := data.createService("busybox", 80, 80, map[string]string{"antrea-e2e": "busybox"}, false)
	require.NoError(t, err)
	stdout, stderr, err := data.runCommandFromPod(testNamespace, "busybox", busyboxContainerName, []string{"nc", svc.Spec.ClusterIP, "80", "-w", "1", "-e", "ls", "/"})
	require.NoError(t, err, fmt.Sprintf("stdout: %s\n, stderr: %s", stdout, stderr))
}

func TestProxyEndpointLifeCycle(t *testing.T) {
	data, err := setupTest(t)
	if err != nil {
		t.Fatalf("Error when setting up test: %v", err)
	}
	defer teardownTest(t, data)

	skipIfProxyDisabled(t, data)

	nodeName := nodeName(1)
	require.NoError(t, data.createNginxPod("nginx", nodeName))
	nginxIP, err := data.podWaitForIP(defaultTimeout, "nginx", testNamespace)
	require.NoError(t, err)
	_, err = data.createNginxService(false)
	require.NoError(t, err)
	agentName, err := data.getAntreaPodOnNode(nodeName)
	require.NoError(t, err)

	keywords := map[int]string{
		42: fmt.Sprintf("nat(dst=%s:80)", nginxIP), // endpointNATTable
	}

	for tableID, keyword := range keywords {
		tableOutput, _, err := data.runCommandFromPod(metav1.NamespaceSystem, agentName, "antrea-agent", []string{"ovs-ofctl", "dump-flows", defaultBridgeName, fmt.Sprintf("table=%d", tableID)})
		require.NoError(t, err)
		require.Contains(t, tableOutput, keyword)
	}

	require.NoError(t, data.deletePodAndWait(defaultTimeout, "nginx"))

	for tableID, keyword := range keywords {
		tableOutput, _, err := data.runCommandFromPod(metav1.NamespaceSystem, agentName, "antrea-agent", []string{"ovs-ofctl", "dump-flows", defaultBridgeName, fmt.Sprintf("table=%d", tableID)})
		require.NoError(t, err)
		require.NotContains(t, tableOutput, keyword)
	}
}

func TestProxyServiceLifeCycle(t *testing.T) {
	data, err := setupTest(t)
	if err != nil {
		t.Fatalf("Error when setting up test: %v", err)
	}
	defer teardownTest(t, data)

	skipIfProxyDisabled(t, data)

	nodeName := nodeName(1)
	require.NoError(t, data.createNginxPod("nginx", nodeName))
	nginxIP, err := data.podWaitForIP(defaultTimeout, "nginx", testNamespace)
	require.NoError(t, err)
	svc, err := data.createNginxService(false)
	require.NoError(t, err)
	agentName, err := data.getAntreaPodOnNode(nodeName)
	require.NoError(t, err)

	keywords := map[int]string{
		41: fmt.Sprintf("nw_dst=%s,tp_dst=80", svc.Spec.ClusterIP), // serviceLBTable
		42: fmt.Sprintf("nat(dst=%s:80)", nginxIP),                 // endpointNATTable
	}
	groupKeyword := fmt.Sprintf("load:0x%s->NXM_NX_REG3[],load:0x%x->NXM_NX_REG4[0..15],load:0x2->NXM_NX_REG4[16..18]", strings.TrimLeft(string(hex.EncodeToString(net.ParseIP(nginxIP).To4())), "0"), 80)
	groupOutput, _, err := data.runCommandFromPod(metav1.NamespaceSystem, agentName, "antrea-agent", []string{"ovs-ofctl", "dump-groups", defaultBridgeName})
	require.NoError(t, err)
	require.Contains(t, groupOutput, groupKeyword)
	for tableID, keyword := range keywords {
		tableOutput, _, err := data.runCommandFromPod(metav1.NamespaceSystem, agentName, "antrea-agent", []string{"ovs-ofctl", "dump-flows", defaultBridgeName, fmt.Sprintf("table=%d", tableID)})
		require.NoError(t, err)
		require.Contains(t, tableOutput, keyword)
	}

	require.NoError(t, data.deleteService("nginx"))
	time.Sleep(time.Second)

	groupOutput, _, err = data.runCommandFromPod(metav1.NamespaceSystem, agentName, "antrea-agent", []string{"ovs-ofctl", "dump-groups", defaultBridgeName})
	require.NoError(t, err)
	require.NotContains(t, groupOutput, groupKeyword)
	for tableID, keyword := range keywords {
		tableOutput, _, err := data.runCommandFromPod(metav1.NamespaceSystem, agentName, "antrea-agent", []string{"ovs-ofctl", "dump-flows", defaultBridgeName, fmt.Sprintf("table=%d", tableID)})
		require.NoError(t, err)
		require.NotContains(t, tableOutput, keyword)
	}
}
