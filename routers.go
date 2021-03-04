package main

import (
	"github.com/beego/beego/v2/server/web"
)

func init() {
	web.Router("/:folder/:id:int", &MainController{})
	web.Router("/:folder/:id:int/:index:int", &AttachmentController{})
	web.Router("/:folder/:id:int/send", &SendController{})
	web.Router("/message_ids/:messageid", &MessageIdController{})
	web.Router("/healthz", &HealthController{})
}
