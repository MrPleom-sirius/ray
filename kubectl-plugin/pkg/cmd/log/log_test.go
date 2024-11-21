package log

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/rest"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/rest/fake"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/remotecommand"
	cmdtesting "k8s.io/kubectl/pkg/cmd/testing"
	"k8s.io/kubectl/pkg/scheme"
)

// Mocked NewSPDYExecutor
var fakeNewSPDYExecutor = func(method string, url *url.URL, inputbuf *bytes.Buffer) (remotecommand.Executor, error) {
	return &fakeExecutor{method: method, url: url, buf: inputbuf}, nil
}

type fakeExecutor struct {
	url    *url.URL
	buf    *bytes.Buffer
	method string
}

// Stream is needed for implementing remotecommand.Execute
func (f *fakeExecutor) Stream(_ remotecommand.StreamOptions) error {
	return nil
}

// downloadRayLogFiles uses StreamWithContext so this is the real function that we are mocking
func (f *fakeExecutor) StreamWithContext(_ context.Context, options remotecommand.StreamOptions) error {
	_, err := io.Copy(options.Stdout, f.buf)
	return err
}

// createFakeTarFile creates the fake tar file that will be used for testing
func createFakeTarFile() (*bytes.Buffer, error) {
	// Create a buffer to hold the tar archive
	tarbuff := new(bytes.Buffer)

	// Create a tar writer
	tw := tar.NewWriter(tarbuff)

	// Define the files/directories to include
	files := []struct {
		ModTime time.Time
		Name    string
		Body    string
		IsDir   bool
		Mode    int64
	}{
		{time.Now(), "/", "", true, 0o755},
		{time.Now(), "file1.txt", "This is the content of file1.txt\n", false, 0o644},
		{time.Now(), "file2.txt", "Content of file2.txt inside subdir\n", false, 0o644},
	}

	// Add each file/directory to the tar archive
	for _, file := range files {
		hdr := &tar.Header{
			Name:    file.Name,
			Mode:    file.Mode,
			ModTime: file.ModTime,
			Size:    int64(len(file.Body)),
		}
		if file.IsDir {
			hdr.Typeflag = tar.TypeDir
		} else {
			hdr.Typeflag = tar.TypeReg
		}

		// Write the header
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}

		// Write the file content (if not a directory)
		if !file.IsDir {
			if _, err := tw.Write([]byte(file.Body)); err != nil {
				return nil, err
			}
		}
	}

	// Close the tar writer
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return tarbuff, nil
}

type FakeRemoteExecutor struct{}

func (dre *FakeRemoteExecutor) CreateExecutor(_ *rest.Config, url *url.URL) (remotecommand.Executor, error) {
	return fakeNewSPDYExecutor("GET", url, new(bytes.Buffer))
}

func TestRayClusterLogComplete(t *testing.T) {
	testStreams, _, _, _ := genericclioptions.NewTestIOStreams()
	fakeClusterLogOptions := NewClusterLogOptions(testStreams)
	fakeArgs := []string{"Expectedoutput"}

	cmd := &cobra.Command{Use: "log"}

	err := fakeClusterLogOptions.Complete(cmd, fakeArgs)

	assert.Equal(t, fakeClusterLogOptions.nodeType, "all")
	assert.Nil(t, err)
	assert.Equal(t, fakeClusterLogOptions.ResourceName, fakeArgs[0])
}

func TestRayClusterLogValidate(t *testing.T) {
	testStreams, _, _, _ := genericclioptions.NewTestIOStreams()

	testNS, testContext, testBT, testImpersonate := "test-namespace", "test-contet", "test-bearer-token", "test-person"

	// Fake directory for kubeconfig
	fakeDir, err := os.MkdirTemp("", "fake-config")
	assert.Nil(t, err)
	defer os.RemoveAll(fakeDir)

	// Set up fake config for kubeconfig
	config := &api.Config{
		Clusters: map[string]*api.Cluster{
			"test-cluster": {
				Server:                "https://fake-kubernetes-cluster.example.com",
				InsecureSkipTLSVerify: true, // For testing purposes
			},
		},
		Contexts: map[string]*api.Context{
			"my-fake-context": {
				Cluster:  "my-fake-cluster",
				AuthInfo: "my-fake-user",
			},
		},
		CurrentContext: "my-fake-context",
		AuthInfos: map[string]*api.AuthInfo{
			"my-fake-user": {
				Token: "", // Empty for testing without authentication
			},
		},
	}

	fakeFile := filepath.Join(fakeDir, ".kubeconfig")

	if err := clientcmd.WriteToFile(*config, fakeFile); err != nil {
		t.Fatalf("Failed to write kubeconfig to temp file: %v", err)
	}

	// Initialize the fake config flag with the fake kubeconfig and values
	fakeConfigFlags := &genericclioptions.ConfigFlags{
		Namespace:        &testNS,
		Context:          &testContext,
		KubeConfig:       &fakeFile,
		BearerToken:      &testBT,
		Impersonate:      &testImpersonate,
		ImpersonateGroup: &[]string{"fake-group"},
	}

	tests := []struct {
		name        string
		opts        *ClusterLogOptions
		expect      string
		expectError string
	}{
		{
			name: "Test validation when no context is set",
			opts: &ClusterLogOptions{
				configFlags:  genericclioptions.NewConfigFlags(false),
				outputDir:    fakeDir,
				ResourceName: "fake-cluster",
				nodeType:     "head",
				ioStreams:    &testStreams,
			},
			expectError: "no context is currently set, use \"kubectl config use-context <context>\" to select a new one",
		},
		{
			name: "Test validation when node type is `random-string`",
			opts: &ClusterLogOptions{
				// Use fake config to bypass the config flag checks
				configFlags:  fakeConfigFlags,
				outputDir:    fakeDir,
				ResourceName: "fake-cluster",
				nodeType:     "random-string",
				ioStreams:    &testStreams,
			},
			expectError: "unknown node type `random-string`",
		},
		{
			name: "Successful validation call",
			opts: &ClusterLogOptions{
				// Use fake config to bypass the config flag checks
				configFlags:  fakeConfigFlags,
				outputDir:    fakeDir,
				ResourceName: "fake-cluster",
				nodeType:     "head",
				ioStreams:    &testStreams,
			},
			expectError: "",
		},
		{
			name: "Validate output directory when no out-dir is set.",
			opts: &ClusterLogOptions{
				// Use fake config to bypass the config flag checks
				configFlags:  fakeConfigFlags,
				outputDir:    "",
				ResourceName: "fake-cluster",
				nodeType:     "head",
				ioStreams:    &testStreams,
			},
			expectError: "",
		},
		{
			name: "Failed validation call with output directory not exist",
			opts: &ClusterLogOptions{
				// Use fake config to bypass the config flag checks
				configFlags:  fakeConfigFlags,
				outputDir:    "randomPath-here",
				ResourceName: "fake-cluster",
				nodeType:     "head",
				ioStreams:    &testStreams,
			},
			expectError: "Directory does not exist. Failed with: stat randomPath-here: no such file or directory",
		},
		{
			name: "Failed validation call with output directory is file",
			opts: &ClusterLogOptions{
				// Use fake config to bypass the config flag checks
				configFlags:  fakeConfigFlags,
				outputDir:    fakeFile,
				ResourceName: "fake-cluster",
				nodeType:     "head",
				ioStreams:    &testStreams,
			},
			expectError: "Path is Not a directory. Please input a directory and try again",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate()
			if tc.expectError != "" {
				assert.Equal(t, tc.expectError, err.Error())
			} else {
				if tc.opts.outputDir == "" {
					assert.Equal(t, tc.opts.ResourceName, tc.opts.outputDir)
				}
				assert.True(t, err == nil)
			}
		})
	}
}

func TestRayClusterLogRun(t *testing.T) {
	tf := cmdtesting.NewTestFactory().WithNamespace("test")
	defer tf.Cleanup()

	fakeDir, err := os.MkdirTemp("", "fake-directory")
	assert.Nil(t, err)
	defer os.RemoveAll(fakeDir)

	testStreams, _, _, _ := genericiooptions.NewTestIOStreams()

	fakeClusterLogOptions := NewClusterLogOptions(testStreams)
	// Uses the mocked executor
	fakeClusterLogOptions.Executor = &FakeRemoteExecutor{}
	fakeClusterLogOptions.ResourceName = "test-cluster"
	fakeClusterLogOptions.outputDir = fakeDir

	// Create list of fake ray heads
	rayHeadsList := &v1.PodList{
		ListMeta: metav1.ListMeta{
			ResourceVersion: "15",
		},
		Items: []v1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-kuberay-head-1",
					Namespace: "test",
					Labels: map[string]string{
						"ray.io/group":    "headgroup",
						"ray.io/clusters": "test-cluster",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "mycontainer",
							Image: "nginx:latest",
						},
					},
				},
				Status: v1.PodStatus{
					Phase: v1.PodRunning,
					PodIP: "10.0.0.1",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-kuberay-head-2",
					Namespace: "test",
					Labels: map[string]string{
						"ray.io/group":    "headgroup",
						"ray.io/clusters": "test-cluster",
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "anothercontainer",
							Image: "busybox:latest",
						},
					},
				},
				Status: v1.PodStatus{
					Phase: v1.PodPending,
				},
			},
		},
	}

	// create logs for multiple head pods and turn them into io streams so they can be returned with the fake client
	fakeLogs := []string{
		"This is some fake log data for first pod.\nStill first pod logs\n",
		"This is some fake log data for second pod.\nStill second pod logs\n",
	}
	logReader1 := io.NopCloser(bytes.NewReader([]byte(fakeLogs[0])))
	logReader2 := io.NopCloser(bytes.NewReader([]byte(fakeLogs[1])))

	// fakes the client and the REST calls.
	codec := scheme.Codecs.LegacyCodec(scheme.Scheme.PrioritizedVersionsAllGroups()...)
	tf.Client = &fake.RESTClient{
		GroupVersion:         v1.SchemeGroupVersion,
		NegotiatedSerializer: resource.UnstructuredPlusDefaultContentConfig().NegotiatedSerializer,
		Client: fake.CreateHTTPClient(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/v1/pods":
				return &http.Response{StatusCode: http.StatusOK, Header: cmdtesting.DefaultHeader(), Body: cmdtesting.ObjBody(codec, rayHeadsList)}, nil
			case "/api/v1/namespaces/test/pods/test-cluster-kuberay-head-1/log":
				return &http.Response{StatusCode: http.StatusOK, Header: cmdtesting.DefaultHeader(), Body: logReader1}, nil
			case "/api/v1/namespaces/test/pods/test-cluster-kuberay-head-2/log":
				return &http.Response{StatusCode: http.StatusOK, Header: cmdtesting.DefaultHeader(), Body: logReader2}, nil
			default:
				t.Fatalf("request url: %#v,and request: %#v", req.URL, req)
				return nil, nil
			}
		}),
	}

	tf.ClientConfigVal = &restclient.Config{
		ContentConfig: restclient.ContentConfig{GroupVersion: &v1.SchemeGroupVersion},
	}

	err = fakeClusterLogOptions.Run(context.Background(), tf)
	assert.Nil(t, err)

	// Check that the two directories are there
	entries, err := os.ReadDir(fakeDir)
	assert.Nil(t, err)
	assert.Equal(t, 2, len(entries))

	assert.Equal(t, "test-cluster-kuberay-head-1", entries[0].Name())
	assert.Equal(t, "test-cluster-kuberay-head-2", entries[1].Name())

	// Check the first directory for the logs
	for ind, entry := range entries {
		currPath := filepath.Join(fakeDir, entry.Name())
		currDir, err := os.ReadDir(currPath)
		assert.Nil(t, err)
		assert.Equal(t, 1, len(currDir))
		openfile, err := os.Open(filepath.Join(currPath, "stdout.log"))
		assert.Nil(t, err)
		actualContent, err := io.ReadAll(openfile)
		assert.Nil(t, err)
		assert.Equal(t, fakeLogs[ind], string(actualContent))
	}
}

func TestDownloadRayLogFiles(t *testing.T) {
	fakeDir, err := os.MkdirTemp("", "fake-directory")
	assert.Nil(t, err)
	defer os.RemoveAll(fakeDir)

	testStreams, _, _, _ := genericiooptions.NewTestIOStreams()

	fakeClusterLogOptions := NewClusterLogOptions(testStreams)
	fakeClusterLogOptions.ResourceName = "test-cluster"
	fakeClusterLogOptions.outputDir = fakeDir

	// create fake tar files to test
	fakeTar, err := createFakeTarFile()
	assert.Nil(t, err)

	// Ray head needed for calling the downloadRayLogFiles command
	rayHead := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-kuberay-head-1",
			Namespace: "test",
			Labels: map[string]string{
				"ray.io/group":    "headgroup",
				"ray.io/clusters": "test-cluster",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "mycontainer",
					Image: "nginx:latest",
				},
			},
		},
		Status: v1.PodStatus{
			Phase: v1.PodRunning,
			PodIP: "10.0.0.1",
		},
	}

	executor, _ := fakeNewSPDYExecutor("GET", &url.URL{}, fakeTar)

	err = fakeClusterLogOptions.downloadRayLogFiles(context.Background(), executor, rayHead)
	assert.Nil(t, err)

	entries, err := os.ReadDir(fakeDir)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(entries))

	// Assert the files
	assert.True(t, entries[0].IsDir())
	files, err := os.ReadDir(filepath.Join(fakeDir, entries[0].Name()))
	assert.Nil(t, err)
	assert.Equal(t, 2, len(files))

	expectedfileoutput := []struct {
		Name string
		Body string
	}{
		{"file1.txt", "This is the content of file1.txt\n"},
		{"file2.txt", "Content of file2.txt inside subdir\n"},
	}

	// Goes through and check the temp directory with the downloaded files
	for ind, file := range files {
		fileInfo, err := file.Info()
		assert.Nil(t, err)
		curr := expectedfileoutput[ind]

		assert.Equal(t, curr.Name, fileInfo.Name())
		openfile, err := os.Open(filepath.Join(fakeDir, entries[0].Name(), file.Name()))
		assert.Nil(t, err)
		actualContent, err := io.ReadAll(openfile)
		assert.Nil(t, err)
		assert.Equal(t, curr.Body, string(actualContent))
	}
}
