package main

import (
	"oam-docker-ipam/command"
	"os"

	"github.com/codegangsta/cli"
)

func main() {
	app := cli.NewApp()
	app.Name = "oam-docker-ipam"
	app.Version = "1.0.0"
	app.Author = "ma chao, kenneth.ye"
	app.Usage = "Docker network IPAM plugin"
	app.Flags = []cli.Flag{
		cli.StringFlag{Name: "cluster-store", Value: "http://127.0.0.1:2379", Usage: "the key/value store endpoint url. [$CLUSTER_STORE]"},
		cli.BoolFlag{Name: "debug", Usage: "debug mode [$DEBUG]"},
	}
	app.Commands = []cli.Command{
		command.NewServerCommand(),
		command.NewIPRangeCommand(),
		command.NewReleaseIPCommand(),
		command.NewHostRangeCommand(),
		command.NewReleaseHostCommand(),
		command.NewCreateNetworkCommand(),
		command.NewVersionCommand(),
	}
	app.Run(os.Args)
}
