//go:build acceptance

// Package acceptance_test runs the image a deployment runs, as docker runs it, and checks the page it serves. The unit tests read a real machine's
// /sys, saved as text, in the test's own process; these are where the built image gets to read it as a container does: as its own user and not
// root, from files it was handed, asked over a published port.
//
// One test hands the image the /sys saved from a machine with two cards and 58 disks, and another gives it nothing, so that it reads the /sys
// docker gives every container, which is how it is deployed. Neither needs a SAS card, and neither can stand in for one: what a card says when a
// cable is pulled or a disk dies is only ever seen on a machine with cards in it.
//
//	make testacc                                             # builds the image and runs these against it
//	SAS_EXPORTER_TEST_PROGRAM_IMAGE=sas-exporter make test-live # or against one already built
package acceptance_test
