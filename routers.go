package main

import (
	"github.com/beego/beego/v2/server/web"
)

func init() {
	web.Router("/:hash", &MainController{})
	web.Router("/:hash/:index:int", &AttachmentController{})
	web.Router("/:hash/send", &SendController{})
	web.Router("/message_ids/:messageid", &MessageIdController{})
	web.Router("/healthz", &HealthController{})
}
