package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/rancher/lasso/pkg/controller"
	extv1 "github.com/rancher/steve/cmd/example/apis/ext.cattle.io/v1"
	wextv1 "github.com/rancher/steve/cmd/example/generated/controllers/ext.cattle.io"
	"github.com/rancher/steve/cmd/example/generated/openapi"
	"github.com/rancher/steve/pkg/ext"
	"github.com/rancher/wrangler/v3/pkg/generic"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/client-go/kubernetes"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func authAsAdmin(req *http.Request) (*authenticator.Response, bool, error) {
	return nil, false, nil
	return &authenticator.Response{
		User: &user.DefaultInfo{
			Name:   "system:masters",
			Groups: []string{"system:masters"},
		},
	}, true, nil
}

func authzAllowAll(ctx context.Context, a authorizer.Attributes) (authorizer.Decision, string, error) {
	fmt.Println("Received request from", a.GetUser())
	return authorizer.DecisionAllow, "", nil
}

func main() {
	scheme := runtime.NewScheme()
	ext.AddToScheme(scheme)
	extv1.AddToScheme(scheme)
	codecs := serializer.NewCodecFactory(scheme)

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", ":0")
	must(err)

	opts := ext.ExtensionAPIServerOptions{
		GetOpenAPIDefinitions: openapi.GetOpenAPIDefinitions,
		Listener:              ln,
		Authorizer:            authorizer.AuthorizerFunc(authzAllowAll),
		Authenticator:         authenticator.RequestFunc(authAsAdmin),
		OpenAPIDefinitionNameReplacements: map[string]string{
			"com.github.rancher.steve.cmd.example.apis": "io.cattle.ext.v1",
		},
	}

	ctx := context.Background()

	apiserver, err := ext.NewExtensionAPIServer(scheme, codecs, opts)
	must(err)

	store := NewDefaultTestStore()
	apiserver.Install("testtypes", store.GroupVersionKind(extv1.SchemeGroupVersion), store)

	if err := apiserver.Run(ctx); err != nil {
		log.Fatal("apiserver.Run", err)
	}

	time.Sleep(2 * time.Second)

	// Get the rest.Config from loopback client which has the following attributes:
	// username: system:apiserver
	// groups: [system:authenticated system:masters]
	// extras: []
	restConfig := apiserver.LoopbackClientConfig()

	// Example 1: Using discovery api from client-go with our loopback client
	client, err := kubernetes.NewForConfig(restConfig)
	must(err)

	fmt.Println(client.DiscoveryClient.ServerGroupsAndResources())

	// Example 2: Use wrangler controller with loopback client
	controllerFactory, err := controller.NewSharedControllerFactoryFromConfigWithOptions(restConfig, scheme, nil)
	must(err)

	factOpts := &generic.FactoryOptions{
		SharedControllerFactory: controllerFactory,
	}

	core, err := wextv1.NewFactoryFromConfigWithOptions(restConfig, factOpts)
	must(err)

	core.Ext().V1().TestType().OnChange(ctx, "my-controller", func(key string, obj *extv1.TestType) (*extv1.TestType, error) {
		fmt.Println("On change", key)
		return obj, nil
	})

	fmt.Println("SharedCacheFactory.Start")
	err = controllerFactory.SharedCacheFactory().Start(ctx)
	must(err)

	fmt.Println("WaitForCacheSync")
	controllerFactory.SharedCacheFactory().WaitForCacheSync(ctx)

	fmt.Println("ControllerFactory.Start")
	err = controllerFactory.Start(ctx, 10)
	must(err)

	<-ctx.Done()
}
