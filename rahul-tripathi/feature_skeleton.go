package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type OPIIntent struct {
	Name                string
	SourceKind          string
	Namespace           string
	UID                 string
	DPUSelector         map[string]string
	ProvisioningProfile string
	ServiceProfiles     []string
	TargetDeviceIDs     []string
}

type DPFObject struct {
	Kind        string
	Namespace   string
	Name        string
	Labels      map[string]string
	Annotations map[string]string
}

type VendorAdapter interface {
	Translate(intent OPIIntent) ([]DPFObject, error)
	MapStatus(dpfObjects []DPFObject) string
}

type NVIDIAAdapter struct {
	DPFNamespace string
}

func (a NVIDIAAdapter) Translate(intent OPIIntent) ([]DPFObject, error) {
	if strings.TrimSpace(intent.Name) == "" {
		return nil, errors.New("OPI intent name is required")
	}
	if strings.TrimSpace(intent.SourceKind) == "" {
		return nil, errors.New("OPI source kind is required")
	}
	if strings.TrimSpace(intent.UID) == "" {
		return nil, errors.New("OPI intent UID is required")
	}
	if strings.TrimSpace(intent.ProvisioningProfile) == "" {
		return nil, errors.New("NVIDIA provisioning profile is required")
	}
	targetDeviceIDs := sortedValues(intent.TargetDeviceIDs)
	if len(targetDeviceIDs) == 0 {
		return nil, errors.New("resolved stable OPI-to-DPF target devices are required")
	}
	if len(intent.DPUSelector) == 0 {
		return nil, errors.New("DPU selector is required to avoid broad accidental reconciliation")
	}
	if err := validateSelector(intent.DPUSelector); err != nil {
		return nil, err
	}

	namespace := a.DPFNamespace
	if namespace == "" {
		namespace = "dpf-operator-system"
	}

	scope := sourceScope(intent)
	targetHash := shortHash(strings.Join(targetDeviceIDs, ","))
	baseLabels := map[string]string{
		"app.kubernetes.io/managed-by": "opi-nvidia-adapter",
		"dpu.opi.io/source-kind":       safeName(intent.SourceKind),
		"dpu.opi.io/source-hash":       shortHash(scope + "/" + intent.Name + "/" + intent.UID),
		"dpu.opi.io/target-set-hash":   targetHash,
	}
	baseAnnotations := map[string]string{
		"dpu.opi.io/source-kind":        intent.SourceKind,
		"dpu.opi.io/source-scope":       scope,
		"dpu.opi.io/source-name":        intent.Name,
		"dpu.opi.io/source-uid":         intent.UID,
		"dpu.opi.io/profile":            intent.ProvisioningProfile,
		"dpu.opi.io/dpu-selector":       formatSelector(intent.DPUSelector),
		"dpu.opi.io/target-device-ids":  strings.Join(targetDeviceIDs, ","),
		"dpu.opi.io/target-device-hash": targetHash,
	}

	objects := []DPFObject{
		{
			Kind:        "DPUSet",
			Namespace:   namespace,
			Name:        childName(intent, "dpuset"),
			Labels:      cloneMap(baseLabels),
			Annotations: cloneMap(baseAnnotations),
		},
	}

	serviceProfiles := cleanProfiles(intent.ServiceProfiles)
	if len(serviceProfiles) > 0 {
		deploymentAnnotations := cloneMap(baseAnnotations)
		deploymentAnnotations["dpu.opi.io/service-order"] = strings.Join(serviceProfiles, ",")
		deploymentAnnotations["dpu.opi.io/service-profiles"] = strings.Join(serviceProfiles, ",")
		deploymentAnnotations["dpu.opi.io/rendered-by"] = "DPUDeployment aggregate"
		objects = append(objects, DPFObject{
			Kind:        "DPUDeployment",
			Namespace:   namespace,
			Name:        childName(intent, "deployment"),
			Labels:      cloneMap(baseLabels),
			Annotations: deploymentAnnotations,
		})

	}

	return objects, nil
}

func (a NVIDIAAdapter) MapStatus(dpfObjects []DPFObject) string {
	if len(dpfObjects) == 0 {
		return "DependencyReady=False: no DPF objects rendered"
	}
	kinds := make([]string, 0, len(dpfObjects))
	for _, object := range dpfObjects {
		kinds = append(kinds, object.Kind+"/"+object.Name)
	}
	sort.Strings(kinds)
	return "Provisioning=True: rendered " + strings.Join(kinds, ", ")
}

func ReconcileNVIDIAIntent(adapter VendorAdapter, intent OPIIntent) (string, error) {
	objects, err := adapter.Translate(intent)
	if err != nil {
		return "Degraded=True: " + err.Error(), err
	}
	return adapter.MapStatus(objects), nil
}

func childName(intent OPIIntent, suffix string) string {
	prefix := safeName(intent.Name)
	hash := shortHash(sourceScope(intent) + "/" + intent.Name + "/" + intent.UID + "/" + suffix)
	maxPrefix := 63 - len(hash) - 1
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	return strings.TrimSuffix(prefix, "-") + "-" + hash
}

func safeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if valid {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	value = strings.Trim(builder.String(), "-")
	if value == "" {
		value = "opi-intent"
	}
	if len(value) > 63 {
		value = strings.TrimSuffix(value[:63], "-")
	}
	return value
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:10]
}

func formatSelector(selector map[string]string) string {
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+selector[key])
	}
	return strings.Join(parts, ",")
}

func validateSelector(selector map[string]string) error {
	for key, value := range selector {
		if strings.TrimSpace(key) == "" {
			return errors.New("DPU selector keys must not be blank")
		}
		if strings.TrimSpace(value) == "" {
			return errors.New("DPU selector values must not be blank")
		}
	}
	return nil
}

func sourceScope(intent OPIIntent) string {
	if strings.TrimSpace(intent.Namespace) == "" {
		return "cluster"
	}
	return "namespace:" + strings.TrimSpace(intent.Namespace)
}

func sortedValues(values []string) []string {
	seen := map[string]bool{}
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			clean = append(clean, value)
			seen[value] = true
		}
	}
	sort.Strings(clean)
	return clean
}

func cleanProfiles(values []string) []string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			clean = append(clean, value)
		}
	}
	return clean
}

func cloneMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func main() {
	status, err := ReconcileNVIDIAIntent(NVIDIAAdapter{}, OPIIntent{
		SourceKind:          "DataProcessingUnitConfig",
		Namespace:           "opi-system",
		Name:                "bluefield-workers",
		UID:                 "7f4f2c6b-0995-41c5-9f03-d4fdbb446b6d",
		DPUSelector:         map[string]string{"dpu.opi.io/vendor": "nvidia"},
		ProvisioningProfile: "bf3-production",
		ServiceProfiles:     []string{"ovn-kubernetes"},
		TargetDeviceIDs:     []string{"serial:MT2345X00001", "pci:0000-03-00.0"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(status)
}
