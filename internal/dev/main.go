package dev

import (
	"errors"
	"flag"
	"fmt"
)

func Main(args []string) error {
	global := flag.NewFlagSet("microstick-dev", flag.ContinueOnError)
	repo := global.String("repository", ".", "source repository location")
	if e := global.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	args = global.Args()
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println("microstick developer tools (Go): bootstrap, extract-blobs, post-build, post-image, verify, check-public, prepare-install, package-release\nUse MICROSTICK_WORKSPACE for external build/private data. extract-blobs uses stock EDL or --stock-image; ADB is not required.")
		return nil
	}
	command := args[0]
	p, e := paths(*repo, command != "check-public" && command != "prepare-install")
	if e != nil {
		return e
	}
	switch command {
	case "bootstrap":
		return Bootstrap(p)
	case "package-release":
		return DistributionCLI(p, args[1:])
	case "extract-blobs":
		return ExtractCLI(p, args[1:])
	case "prepare-install":
		return PersonalizeCLI(p, args[1:])
	case "check-public":
		return CheckPublic(p)
	case "post-build":
		if len(args) != 2 {
			return errors.New("post-build requires target directory")
		}
		return PostBuild(p, args[1])
	case "post-image":
		if len(args) != 2 {
			return errors.New("post-image requires images directory")
		}
		return PostImage(p, args[1])
	case "verify":
		return Verify(p)
	case "qdl-bootstrap":
		_, e := ensureQDL(p)
		return e
	default:
		return fmt.Errorf("unknown developer command: %s", command)
	}
}
