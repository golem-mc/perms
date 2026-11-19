// Command server runs golem with the LuckPerms plugin built in.
package main

import (
	"github.com/golem-mc/golem/app"
	_ "github.com/golem-mc/perms"
)

func main() { app.Main() }
