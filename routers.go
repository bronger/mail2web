package main

import (
	"github.com/beego/beego/v2/server/web"
)

func init() {
	web.Router("/:hash", &MainController{})
	web.Router("/:hash/:index:int", &AttachmentController{})
	web.Router("/restricted/:hash/send", &SendController{})
	web.Router("/restricted/my_mails", &MyMailsController{})
	web.Router("/healthz", &HealthController{})
}
