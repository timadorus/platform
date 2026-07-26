package e2eutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// imageComponents lists every image this suite must build, matching the existing
// Dockerfile.<name> naming convention at the repo root (command-api, query-api, and
// projector are the platform's public binaries; migrate is required too — the chart's
// migration Job can't run without its own image, even though it wasn't among the three named
// services).
var imageComponents = []string{"command-api", "query-api", "projector", "migrate"}

// ImageTags maps component name (e.g. "command-api") to the SHA256 digest tag it was built
// and loaded under.
type ImageTags map[string]string

// BuildTagLoadImages builds Dockerfile.<name> for every component in imageComponents, tags
// each with its own content digest (the image's SHA256 ID, hex only — Kubernetes image tags
// cannot contain the "sha256:" prefix's colon), and kind-loads it into clusterName. It returns
// the digest tag used for each component so the caller can pass them straight into the Helm
// install, with pullPolicy: IfNotPresent (the image was loaded directly onto the node, never
// pulled from a registry).
func BuildTagLoadImages(clusterName string) (ImageTags, error) {
	tags := make(ImageTags, len(imageComponents))

	for _, name := range imageComponents {
		repository := "timadorus/" + name
		dockerfile := "Dockerfile." + name

		if _, err := Run(exec.Command("docker", "build", "-f", dockerfile, "-t", repository+":build", ".")); err != nil {
			return nil, fmt.Errorf("e2eutil: docker build %s: %w", dockerfile, err)
		}

		digestOutput, err := Run(exec.Command("docker", "inspect", "--format={{.Id}}", repository+":build"))
		if err != nil {
			return nil, fmt.Errorf("e2eutil: inspect %s: %w", repository, err)
		}
		digest := strings.TrimSpace(digestOutput)
		tag := strings.TrimPrefix(digest, "sha256:")
		if tag == digest {
			return nil, fmt.Errorf("e2eutil: unexpected image ID format for %s: %q", repository, digest)
		}

		taggedImage := repository + ":" + tag
		if _, err := Run(exec.Command("docker", "tag", repository+":build", taggedImage)); err != nil {
			return nil, fmt.Errorf("e2eutil: docker tag %s: %w", repository, err)
		}

		if _, err := Run(exec.Command("kind", "load", "docker-image", taggedImage, "--name", clusterName)); err != nil {
			return nil, fmt.Errorf("e2eutil: kind load %s: %w", taggedImage, err)
		}

		tags[name] = tag
	}

	return tags, nil
}
