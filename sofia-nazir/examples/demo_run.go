package main

import (
	"fmt"

	opiv1alpha1 "opi-nvidia-adapter/api/v1alpha1"
	"opi-nvidia-adapter/pkg/adapter"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	fmt.Println("=====================================================================")
	fmt.Println("OPI DPU Operator NVIDIA Adapter: Live Demo Translation")
	fmt.Println("=====================================================================")

	// Defined sample input DPUCluster #Safiya :)
	cluster := &opiv1alpha1.DPUCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-nvidia-dpu-cluster",
			Namespace: "default",
		},
		Spec: opiv1alpha1.DPUClusterSpec{
			Vendor:             "nvidia",
			BFB:                "http://firmware.internal/bluefield/bfb-v3.0.bfb",
			DpuFlavor:          "high-throughput-networking",
			NetworkOffloadMode: "ovn-kubernetes",
			VpcName:            "production-vpc",
			NodeSelector: map[string]string{
				"hardware":     "bluefield-3",
				"cluster-role": "worker",
			},
		},
	}

	fmt.Printf("[INPUT] OPI DPUCluster CR Spec:\n")
	fmt.Printf("  - Name: %s\n", cluster.Name)
	fmt.Printf("  - Vendor: %s\n", cluster.Spec.Vendor)
	fmt.Printf("  - BFB Bootstream URL: %s\n", cluster.Spec.BFB)
	fmt.Printf("  - DPU Flavor: %s\n", cluster.Spec.DpuFlavor)
	fmt.Printf("  - Network Offload Mode: %s\n", cluster.Spec.NetworkOffloadMode)
	fmt.Printf("  - Target VPC: %s\n", cluster.Spec.VpcName)
	fmt.Printf("  - Node Selector: %v\n\n", cluster.Spec.NodeSelector)

	fmt.Println("Translating fields using adapter...")

	// 2. Perform translation using translator module
	dpuset := adapter.TranslateDPUClusterToDPUSet(cluster)
	dpuservice := adapter.TranslateDPUClusterToDPUService(cluster, dpuset.Name)

	// 3. Display translated resources
	fmt.Println("=====================================================================")
	fmt.Printf("[OUTPUT 1] Generated NVIDIA DPF DPUSet Spec:\n")
	fmt.Printf("  - Name: %s\n", dpuset.Name)
	fmt.Printf("  - BFB Bootstream: %s\n", dpuset.Spec.DPUTemplate.Spec.BFB)
	fmt.Printf("  - Flavor Preset: %s\n", dpuset.Spec.DPUTemplate.Spec.DPUFlavor)
	fmt.Printf("  - Node Selector: %v\n\n", dpuset.Spec.DPUNodeSelector)

	fmt.Printf("[OUTPUT 2] Generated NVIDIA DPF DPUService Spec:\n")
	fmt.Printf("  - Name: %s\n", dpuservice.Name)
	fmt.Printf("  - Service ID: %v\n", dpuservice.Spec.ServiceID)
	fmt.Printf("  - Deploy In Cluster: %v\n", dpuservice.Spec.DeployInCluster)
	fmt.Println("=====================================================================")
	fmt.Println("Success: Translation works perfectly!")
}
