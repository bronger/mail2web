#!/usr/bin/env python

import sys, email.parser, hashlib, base64, os, argparse


parser = argparse.ArgumentParser(description="Calculating links to mails.")
parser.add_argument("path", help="absolute path to the mail file")
parser.add_argument("--access", choices=("full",), help="Mode of access.  Defaults to all thread mails which are older.")
args = parser.parse_args()


url_root = "https://{}{}/".format(os.environ["DOMAIN"], os.environ.get("ROOT_URL", ""))
pepper = open(os.environ.get("SECRET_KEY_PATH", "/var/lib/mail2web_secrets/secret_key")).read().strip()


def hash_id(message_id, salt=""):
    hash_ = hashlib.sha256()
    hash_.update(pepper.encode())
    if salt:
        hash_.update((salt + ">").encode())
    hash_.update(message_id.encode())
    return base64.urlsafe_b64encode(hash_.digest())[:10].decode()


message_id = email.parser.Parser().parse(open(sys.argv[1]))["Message-ID"].strip().strip("<>")
if not args.access:
    url = url_root + hash_id(message_id)
else:
    query_key = "token" + args.access.capitalize()
    url = url_root + hash_id(message_id) + f"?{query_key}=" + hash_id(message_id, args.access)
print(url)
