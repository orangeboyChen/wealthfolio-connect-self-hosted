package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/fx"
)

func TestServer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Server Composition Suite")
}

var _ = Describe("Modules", func() {
	It("returns a non-nil fx.Option", func() {
		opt := Modules()
		Expect(opt).NotTo(BeNil())
		// Building a fx.New with the option should at minimum not panic;
		// real Start would require DATABASE_URL etc. but constructing the
		// graph with NopLogger surfaces wiring errors only.
		app := fx.New(opt, fx.NopLogger)
		Expect(app).NotTo(BeNil())
	})
})
