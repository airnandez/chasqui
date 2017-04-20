package main

import (
	"flag"
	"os"
)

func main() {

	commands := map[string]command{
		"driver": driverCmd(),
		"server": serverCmd(),
		"client": clientCmd(),
	}

	fset := flag.NewFlagSet("chasqui", flag.ExitOnError)
	version := fset.Bool("version", false, "print version number")
	help := fset.Bool("help", false, "print help")
	fset.Usage = func() { printUsage(os.Stderr, usageShort) }

	fset.Parse(os.Args[1:])
	if *version {
		printVersion(os.Stderr)
		os.Exit(0)
	}
	if *help {
		printUsage(os.Stderr, usageLong)
		os.Exit(0)
	}
	args := fset.Args()
	if len(args) == 0 {
		fset.Usage()
		return
	}
	if cmd, ok := commands[args[0]]; !ok {
		errlog.Printf("'%s' is not an accepted command\n", args[0])
		fset.Usage()
		os.Exit(1)
	} else if err := cmd.run(args); err != nil {
		errlog.Printf("%s\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

type command struct {
	fset *flag.FlagSet
	run  func(args []string) error
}
