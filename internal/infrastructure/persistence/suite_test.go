package persistence_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPersistence(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Persistence Suite")
}
