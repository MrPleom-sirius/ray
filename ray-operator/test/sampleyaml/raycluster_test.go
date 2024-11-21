package sampleyaml

import (
	"path"
	"testing"

	. "github.com/onsi/gomega"

	rayv1 "github.com/ray-project/kuberay/ray-operator/apis/ray/v1"
	. "github.com/ray-project/kuberay/ray-operator/test/support"
)

func TestRayCluster(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "ray-cluster.autoscaler-v2.yaml",
		},
		{
			name: "ray-cluster.autoscaler.yaml",
		},
		{
			name: "ray-cluster.complete.yaml",
		},
		{
			name: "ray-cluster.custom-head-service.yaml",
		},
		{
			name: "ray-cluster.embed-grafana.yaml",
		},
		{
			name: "ray-cluster.external-redis-uri.yaml",
		},
		{
			name: "ray-cluster.external-redis.yaml",
		},
		{
			name: "ray-cluster.head-command.yaml",
		},
		{
			name: "ray-cluster.heterogeneous.yaml",
		},
		{
			name: "ray-cluster.overwrite-command.yaml",
		},
		{
			name: "ray-cluster.py-spy.yaml",
		},
		{
			name: "ray-cluster.sample.yaml",
		},
		{
			name: "ray-cluster.separate-ingress.yaml",
		},
		{
			name: "ray-cluster.tls.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			test := With(t)
			g := NewWithT(t)

			yamlFilePath := path.Join(GetSampleYAMLDir(test), tt.name)
			namespace := test.NewTestNamespace()
			test.StreamKubeRayOperatorLogs()
			rayClusterFromYaml := DeserializeRayClusterYAML(test, yamlFilePath)
			KubectlApplyYAML(test, yamlFilePath, namespace.Name)

			rayCluster, err := GetRayCluster(test, namespace.Name, rayClusterFromYaml.Name)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(rayCluster).NotTo(BeNil())

			test.T().Logf("Waiting for RayCluster %s/%s to be ready", namespace.Name, rayCluster.Name)
			g.Eventually(RayCluster(test, namespace.Name, rayCluster.Name), TestTimeoutMedium).
				Should(WithTransform(RayClusterState, Equal(rayv1.Ready)))
			rayCluster, err = GetRayCluster(test, namespace.Name, rayCluster.Name)
			g.Expect(err).NotTo(HaveOccurred())

			// Check if the RayCluster created correct number of pods
			var desiredWorkerReplicas int32
			if rayCluster.Spec.WorkerGroupSpecs != nil {
				for _, workerGroupSpec := range rayCluster.Spec.WorkerGroupSpecs {
					desiredWorkerReplicas += *workerGroupSpec.Replicas
				}
			}
			g.Eventually(WorkerPods(test, rayCluster), TestTimeoutShort).Should(HaveLen(int(desiredWorkerReplicas)))
			g.Expect(GetRayCluster(test, namespace.Name, rayCluster.Name)).To(WithTransform(RayClusterDesiredWorkerReplicas, Equal(desiredWorkerReplicas)))

			// Check if the head pod is ready
			g.Eventually(HeadPod(test, rayCluster), TestTimeoutShort).Should(WithTransform(IsPodRunningAndReady, BeTrue()))

			// Check if all worker pods are ready
			g.Eventually(WorkerPods(test, rayCluster), TestTimeoutShort).Should(WithTransform(AllPodsRunningAndReady, BeTrue()))

			// Check that all pods can submit jobs
			g.Eventually(SubmitJobsToAllPods(test, rayCluster), TestTimeoutShort).Should(Succeed())
		})
	}
}
