package router

import (
	"net/http"
	"time"

	g "github.com/onsi/ginkgo"
	o "github.com/onsi/gomega"

	kapierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"

	routeclientset "github.com/openshift/client-go/route/clientset/versioned"
	exutil "github.com/openshift/origin/test/extended/util"
	"github.com/openshift/origin/test/extended/util/url"
)

var _ = g.Describe("[Conformance][Area:Networking][Feature:Router]", func() {
	defer g.GinkgoRecover()
	var (
		host, ns string
		oc       *exutil.CLI

		configPath = exutil.FixturePath("testdata", "ingress.yaml")
	)

	// this hook must be registered before the framework namespace teardown
	// hook
	g.AfterEach(func() {
		if g.CurrentGinkgoTestDescription().Failed {
			currlabel := "router=router"
			for _, ns := range []string{"default", "openshift-ingress", "tectonic-ingress"} {
				//Search the router by label
				if ns == "openshift-ingress" {
					currlabel = "router=router-default"
				}
				exutil.DumpPodLogsStartingWithInNamespace("router", ns, oc.AsAdmin())
				selector, err := labels.Parse(currlabel)
				if err != nil {
					panic(err)
				}
				exutil.DumpPodsCommand(oc.AdminKubeClient(), ns, selector, "cat /var/lib/haproxy/router/routes.json /var/lib//var/lib/haproxy/conf/haproxy.config")
			}
		}
	})

	oc = exutil.NewCLI("router-stress", exutil.KubeConfigPath())

	g.BeforeEach(func() {
		var err error
		host, err = waitForFirstRouterEndpointIP(oc)
		if kapierrs.IsNotFound(err) {
			g.Skip("no router installed on the cluster")
			return
		}
		o.Expect(err).NotTo(o.HaveOccurred())

		ns = oc.KubeFramework().Namespace.Name
	})

	g.Describe("The HAProxy router", func() {
		g.It("should respond with 503 to unrecognized hosts", func() {
			t := url.NewTester(oc.AdminKubeClient(), ns)
			defer t.Close()
			t.Within(
				time.Minute,
				url.Expect("GET", "https://www.google.com").Through(host).SkipTLSVerification().HasStatusCode(503),
				url.Expect("GET", "http://www.google.com").Through(host).HasStatusCode(503),
			)
		})

		g.It("should serve routes that were created from an ingress", func() {
			g.By("deploying an ingress rule")
			err := oc.Run("create").Args("-f", configPath).Execute()
			o.Expect(err).NotTo(o.HaveOccurred())

			g.By("waiting for the ingress rule to be converted to routes")
			client := routeclientset.NewForConfigOrDie(oc.AdminConfig())
			err = wait.Poll(time.Second, time.Minute, func() (bool, error) {
				routes, err := client.Route().Routes(ns).List(metav1.ListOptions{})
				if err != nil {
					return false, err
				}
				return len(routes.Items) == 4, nil
			})
			o.Expect(err).NotTo(o.HaveOccurred())

			g.By("verifying the router reports the correct behavior")
			t := url.NewTester(oc.AdminKubeClient(), ns)
			defer t.Close()
			t.Within(
				3*time.Minute,
				url.Expect("GET", "http://1.ingress-test.com/test").Through(host).HasStatusCode(200),
				url.Expect("GET", "http://1.ingress-test.com/other/deep").Through(host).HasStatusCode(200),
				url.Expect("GET", "http://1.ingress-test.com/").Through(host).HasStatusCode(503),
				url.Expect("GET", "http://2.ingress-test.com/").Through(host).HasStatusCode(200),
				url.Expect("GET", "https://3.ingress-test.com/").Through(host).SkipTLSVerification().HasStatusCode(200),
				url.Expect("GET", "http://3.ingress-test.com/").Through(host).RedirectsTo("https://3.ingress-test.com/", http.StatusFound),
			)
		})
	})
})

func waitForFirstRouterEndpointIP(oc *exutil.CLI) (string, error) {
	// wait for at least one router endpoint to be up router endpoints to show up
	var host string
	err := wait.PollImmediate(2*time.Second, 120*time.Second, func() (bool, error) {
		currlabel := "router=router"
		for _, ns := range []string{"default", "openshift-ingress", "tectonic-ingress"} {
			//Search the router by label
			if ns == "openshift-ingress" {
				currlabel = "router=router-default"
			}
			podList, err := oc.AdminKubeClient().CoreV1().Pods(ns).List(metav1.ListOptions{LabelSelector: currlabel})
			if podList.Items != nil && len(podList.Items) > 0 {
				host = podList.Items[0].Status.PodIP
				return true, nil
			}
			o.Expect(err).NotTo(o.HaveOccurred())
		}
		return false, nil
	})
	return host, err
}
