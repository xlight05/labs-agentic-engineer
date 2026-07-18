// Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package codingagent

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k8sclient "github.com/wso2/aep/aep-api/internal/clients/k8s"
	"github.com/wso2/aep/aep-api/internal/platform/tenant"
)

// k8sJobRunnerSA is the ServiceAccount created in the org's data-plane
// remote-worker namespace. The coding-agent Job pod runs as this SA.
const k8sJobRunnerSA = "remote-worker-runner"

// K8sJobInput collects everything needed to dispatch one direct K8s Job.
type K8sJobInput struct {
	RunName       string
	OrgID         string
	OrgUUID       string // used to derive the data-plane namespace
	ProjectID     string
	Component     string
	ExecutionID   string // stamped as AEP_TASK_ID (naming debt: carries the execution id)
	RepoURL       string
	Prompt        string
	IdentityName  string
	IdentityEmail string
	IdentityLogin string
	Bearer        string
	SkillsRepoURL string
}

// K8sJobDispatcher dispatches coding-agent runs as direct K8s Jobs using the
// in-cluster controller-runtime client. No cluster-gateway-proxy or SM-API
// dependency: the Anthropic key is written directly to the data-plane namespace,
// and the runner authenticates to GitHub via AEP_BEARER -> aep-api callback.
type K8sJobDispatcher struct {
	client      client.Client
	anthropic   AnthropicKeyReader
	platformURL string
	runnerImage string
}

// NewK8sJobDispatcher constructs the dispatcher. All parameters are required.
func NewK8sJobDispatcher(cl client.Client, anthropic AnthropicKeyReader, platformURL, runnerImage string) *K8sJobDispatcher {
	return &K8sJobDispatcher{client: cl, anthropic: anthropic, platformURL: platformURL, runnerImage: runnerImage}
}

// Dispatch ensures the data-plane namespace and service account exist, writes the
// Anthropic secret, then creates the coding-agent Job. Returns the run name.
func (d *K8sJobDispatcher) Dispatch(ctx context.Context, in K8sJobInput) (string, error) {
	ns := tenant.RemoteWorkerNamespace(in.OrgUUID)

	if err := d.ensureNamespace(ctx, ns, in.OrgID); err != nil {
		return "", fmt.Errorf("k8s-job: ensure namespace %s: %w", ns, err)
	}
	if err := d.ensureServiceAccount(ctx, ns, in.OrgID); err != nil {
		return "", fmt.Errorf("k8s-job: ensure service account: %w", err)
	}

	anthropicKey, err := d.anthropic.AnthropicKeyFor(ctx, in.OrgID)
	if err != nil {
		return "", fmt.Errorf("k8s-job: anthropic key for org %q: %w", in.OrgID, err)
	}
	if err := d.applySecret(ctx, ns, tenant.AnthropicSecretName, in.OrgID, map[string]string{
		"ANTHROPIC_API_KEY": anthropicKey,
	}); err != nil {
		return "", fmt.Errorf("k8s-job: apply anthropic secret: %w", err)
	}

	manifest, err := Build(JobInputs{
		RunName:             in.RunName,
		OrgNS:               ns,
		TaskID:              in.ExecutionID,
		OrgID:               in.OrgID,
		ProjectID:           in.ProjectID,
		ComponentName:       in.Component,
		RunnerImage:         d.runnerImage,
		ServiceAccountName:  k8sJobRunnerSA,
		AnthropicSecretName: tenant.AnthropicSecretName,
		// GitHubSecretName intentionally empty: the runner authenticates to
		// GitHub via AEP_BEARER -> aep-api /credentials/* callback.
		RepoURL:       in.RepoURL,
		Prompt:        in.Prompt,
		IdentityName:  in.IdentityName,
		IdentityEmail: in.IdentityEmail,
		IdentityLogin: in.IdentityLogin,
		GitServiceURL: d.platformURL,
		CallbackURL:   d.platformURL,
		Bearer:        in.Bearer,
		SkillsRepoURL: in.SkillsRepoURL,
	})
	if err != nil {
		return "", fmt.Errorf("k8s-job: build job manifest: %w", err)
	}
	if err := d.applyJob(ctx, ns, in.RunName, manifest); err != nil {
		return "", fmt.Errorf("k8s-job: apply job: %w", err)
	}

	slog.InfoContext(ctx, "k8s-job: dispatched coding-agent job",
		"org", in.OrgID, "namespace", ns, "job", in.RunName)
	return in.RunName, nil
}

func (d *K8sJobDispatcher) ensureNamespace(ctx context.Context, ns, orgID string) error {
	obj := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "aep-api",
				"aep.io/org":                   orgID,
			},
		},
	}
	return d.client.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner(k8sclient.FieldOwner))
}

func (d *K8sJobDispatcher) ensureServiceAccount(ctx context.Context, ns, orgID string) error {
	obj := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ServiceAccount"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      k8sJobRunnerSA,
			Namespace: ns,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "aep-api",
				"aep.io/org":                   orgID,
			},
		},
	}
	return d.client.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner(k8sclient.FieldOwner))
}

func (d *K8sJobDispatcher) applySecret(ctx context.Context, ns, name, orgID string, data map[string]string) error {
	obj := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "aep-api",
				"aep.io/org":                   orgID,
			},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
	return d.client.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner(k8sclient.FieldOwner))
}

// applyJob deletes any stale Job with the same name (re-dispatch within the minute
// bucket reuses the same name) then creates the new Job via the unstructured client
// so no batch/v1 scheme registration is required.
func (d *K8sJobDispatcher) applyJob(ctx context.Context, ns, runName string, manifest map[string]any) error {
	stale := &unstructured.Unstructured{}
	stale.SetAPIVersion("batch/v1")
	stale.SetKind("Job")
	stale.SetName(runName)
	stale.SetNamespace(ns)
	if err := d.client.Delete(ctx, stale); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete stale job: %w", err)
	}
	obj := &unstructured.Unstructured{Object: manifest}
	return d.client.Create(ctx, obj)
}
