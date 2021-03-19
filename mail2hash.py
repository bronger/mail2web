#!/usr/bin/env python

import sys, email.parser, hashlib, base64


url_root = "https://mails.example.com/"
pepper = ""


def hash_id(message_id, salt=""):
    hash_ = hashlib.sha256()
    hash_.update(pepper.encode())
    if salt:
        hash_.update((salt + ">").encode())
    hash_.update(message_id.encode())
    return base64.urlsafe_b64encode(hash_.digest())[:10].decode()


message_id = email.parser.Parser().parse(open(sys.argv[1]))["Message-ID"].strip().strip("<>")
print(url_root + hash_id(message_id))
print(url_root + hash_id(message_id) + "?tokenFull=" + hash_id(message_id, "full"))
