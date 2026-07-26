//go:build e2e

package e2e

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	e2eutil "github.com/timadorus/platform/test/e2e/internal"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Timadorus Platform e2e Suite")
}

var env *e2eutil.Environment

var _ = BeforeSuite(func() {
	var err error
	env, err = e2eutil.Setup()
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	env.Teardown()
})
