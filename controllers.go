package main

import (
	"fmt"

	"github.com/beego/beego/v2/server/web"
)

type MainController struct {
	web.Controller
}

func (this *MainController) Get() {
	folder := this.Ctx.Input.Param(":folder")
	id := this.Ctx.Input.Param(":id")
	fmt.Println(folder, id)
	this.TplName = "index.tpl"
}
