package main

import (
	"github.com/beego/beego/v2/server/web"
)

func init() {
	web.Router("/:hash/?:messageid/:index:int", &AttachmentController{})
	web.Router("/:hash/?:messageid", &MainController{})
	web.Router("/restricted/:hash/?:messageid/send", &SendController{})
	web.Router("/restricted/my_mails", &MyMailsController{})
	web.Router("/restricted/request/?:messageid", &MailRequestController{})
	web.Router("/healthz", &HealthController{})
}
