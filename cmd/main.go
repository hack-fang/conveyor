package main

import (
	"github.com/chenjiandongx/conveyor"
)

func main() {
	porter := conveyor.NewFileBeatPorter(nil)
	cy := conveyor.NewConveyor("")
	cy.RegisterPorter(porter)
	cy.Run()
}
