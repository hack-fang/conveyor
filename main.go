package main

import (
	conveyor "github.com/chenjiandongx/conveyor/pkg"
)

func main() {
	porter := conveyor.NewFileBeatPorter(nil)
	cy := conveyor.NewConveyor("")
	cy.RegisterPorter(porter)
	cy.Run()
}
