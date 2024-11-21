package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/ray-project/kuberay/kubectl-plugin/pkg/util"
	"github.com/ray-project/kuberay/kubectl-plugin/pkg/util/client"
	"github.com/ray-project/kuberay/kubectl-plugin/pkg/util/completion"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/kubectl/pkg/cmd/portforward"
	cmdutil "k8s.io/kubectl/pkg/cmd/util"
	"k8s.io/kubectl/pkg/util/templates"
)

type appPort struct {
	name string
	port int
}

type SessionOptions struct {
	configFlags  *genericclioptions.ConfigFlags
	ioStreams    *genericiooptions.IOStreams
	ResourceType util.ResourceType
	ResourceName string
	Namespace    string
}

var (
	dashboardPort = appPort{
		name: "Ray Dashboard",
		port: 8265,
	}
	clientPort = appPort{
		name: "Ray Interactive Client",
		port: 10001,
	}
	servePort = appPort{
		name: "Ray Serve",
		port: 8000,
	}
)

var (
	sessionLong = templates.LongDesc(`
		Forward local ports to the Ray resources.

		Forward different local ports depending on the resource type: RayCluster, RayJob, or RayService.
	`)

	sessionExample = templates.Examples(`
		# Without specifying the resource type, forward local ports to the RayCluster resource
		kubectl ray session my-raycluster

		# Forward local ports to the RayCluster resource
		kubectl ray session raycluster/my-raycluster

		# Forward local ports to the RayCluster used for the RayJob resource
		kubectl ray session rayjob/my-rayjob

		# Forward local ports to the RayCluster used for the RayService resource
		kubectl ray session rayservice/my-rayservice
	`)
)

func NewSessionOptions(streams genericiooptions.IOStreams) *SessionOptions {
	configFlags := genericclioptions.NewConfigFlags(true)
	return &SessionOptions{
		ioStreams:   &streams,
		configFlags: configFlags,
	}
}

func NewSessionCommand(streams genericiooptions.IOStreams) *cobra.Command {
	options := NewSessionOptions(streams)
	factory := cmdutil.NewFactory(options.configFlags)

	cmd := &cobra.Command{
		Use:               "session (RAYCLUSTER | TYPE/NAME)",
		Short:             "Forward local ports to the Ray resources.",
		Long:              sessionLong,
		Example:           sessionExample,
		ValidArgsFunction: completion.RayClusterResourceNameCompletionFunc(factory),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := options.Complete(cmd, args); err != nil {
				return err
			}
			if err := options.Validate(); err != nil {
				return err
			}
			return options.Run(cmd.Context(), factory)
		},
	}
	options.configFlags.AddFlags(cmd.Flags())
	return cmd
}

func (options *SessionOptions) Complete(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return cmdutil.UsageErrorf(cmd, "%s", cmd.Use)
	}

	typeAndName := strings.Split(args[0], "/")
	if len(typeAndName) == 1 {
		options.ResourceType = util.RayCluster
		options.ResourceName = typeAndName[0]
	} else {
		if len(typeAndName) != 2 || typeAndName[1] == "" {
			return cmdutil.UsageErrorf(cmd, "invalid resource type/name: %s", args[0])
		}

		switch typeAndName[0] {
		case string(util.RayCluster):
			options.ResourceType = util.RayCluster
		case string(util.RayJob):
			options.ResourceType = util.RayJob
		case string(util.RayService):
			options.ResourceType = util.RayService
		default:
			return cmdutil.UsageErrorf(cmd, "unsupported resource type: %s", typeAndName[0])
		}

		options.ResourceName = typeAndName[1]
	}

	if *options.configFlags.Namespace == "" {
		options.Namespace = "default"
	} else {
		options.Namespace = *options.configFlags.Namespace
	}

	return nil
}

func (options *SessionOptions) Validate() error {
	// Overrides and binds the kube config then retrieves the merged result
	config, err := options.configFlags.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return fmt.Errorf("Error retrieving raw config: %w", err)
	}
	if len(config.CurrentContext) == 0 {
		return fmt.Errorf("no context is currently set, use %q to select a new one", "kubectl config use-context <context>")
	}
	return nil
}

func (options *SessionOptions) Run(ctx context.Context, factory cmdutil.Factory) error {
	k8sClient, err := client.NewClient(factory)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	svcName, err := k8sClient.GetRayHeadSvcName(ctx, options.Namespace, options.ResourceType, options.ResourceName)
	if err != nil {
		return err
	}
	fmt.Printf("Forwarding ports to service %s\n", svcName)

	var appPorts []appPort
	switch options.ResourceType {
	case util.RayCluster:
		appPorts = []appPort{dashboardPort, clientPort}
	case util.RayJob:
		appPorts = []appPort{dashboardPort}
	case util.RayService:
		appPorts = []appPort{dashboardPort, servePort}
	default:
		return fmt.Errorf("unsupported resource type: %s", options.ResourceType)
	}

	portForwardCmd := portforward.NewCmdPortForward(factory, *options.ioStreams)
	args := []string{"service/" + svcName}
	for _, appPort := range appPorts {
		args = append(args, fmt.Sprintf("%d:%d", appPort.port, appPort.port))
	}
	portForwardCmd.SetArgs(args)

	for _, appPort := range appPorts {
		fmt.Printf("%s: http://localhost:%d\n", appPort.name, appPort.port)
	}
	fmt.Println()

	if err := portForwardCmd.ExecuteContext(ctx); err != nil {
		return fmt.Errorf("failed to port-forward: %w", err)
	}

	return nil
}
