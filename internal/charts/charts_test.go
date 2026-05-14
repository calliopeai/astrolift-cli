package charts

import "testing"

func TestCloudFromProviderPlugin(t *testing.T) {
	cases := []struct {
		slug string
		want Cloud
	}{
		{"aws", CloudAWS},
		{"eks", CloudAWS},
		{"EKS", CloudAWS},
		{"gcp", CloudGCP},
		{"gke", CloudGCP},
		{"azure", CloudAzure},
		{"aks", CloudAzure},
		{"k8s_native", CloudK8s},
		{"k3s", CloudK8s},
		{"", CloudK8s},             // unknown falls back to k8s
		{"random_cloud", CloudK8s}, // unknown falls back to k8s
	}
	for _, c := range cases {
		t.Run(c.slug, func(t *testing.T) {
			got := CloudFromProviderPlugin(c.slug)
			if got != c.want {
				t.Fatalf("CloudFromProviderPlugin(%q) = %q, want %q", c.slug, got, c.want)
			}
		})
	}
}

func TestCloudValidate(t *testing.T) {
	for _, c := range AllClouds {
		if err := c.Validate(); err != nil {
			t.Fatalf("AllClouds entry %q failed Validate: %v", c, err)
		}
	}
	if err := Cloud("nimbus").Validate(); err == nil {
		t.Fatal("Cloud(\"nimbus\").Validate() = nil, want error")
	}
}

func TestIsCloudValuesFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"values.aws.yaml", true},
		{"values.k8s.yaml", true},
		{"values.yaml", false},
		{"Chart.yaml", false},
		{"templates/values.aws.yaml", false}, // nested file, not a root overlay
		{"README.md", false},
	}
	for _, c := range cases {
		got := isCloudValuesFile(c.path)
		if got != c.want {
			t.Fatalf("isCloudValuesFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
