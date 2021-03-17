Purpose
=======

mail2web exposes an email archive to the Web consisting of folders with
RFC 5322 files.  This way, you can give your mails persistent identifiers while
at the same time you are able to show them – along with their thread and
attachments – to others.


Security
========

Mails may be very sensitive, so security in mail2web is crucial.  This is
achieved by addressing all mail using a salted hash like this::

  https://mymails.example.com/87g46e5i78

Let us call the mail addressed by this link the *origin mail*.  The link also
exposes the thread associated with the origin mail to everyone that has that
link, but only those mails that are older than the origin mail.

The hash is base64-URL-encoded with 10 characters, so it has :math:`2^{60}`
possible values.  If you have e.g. :math:`2^{17}` ≈ 130,000 mails, attackers
will have a hit each 8,796,093,022,208 tries, on average.


Known weaknesses
----------------

1. If a new mail arrives with a timestamp older than the origin mail, it is
   also exposed by the link to the origin mail.  The mail should belong to the
   same topic, and mails with ealier timestamps should not arrive later.
   Still, this means that newly arrived mails may become immediately
   accessible.
2. If you grant certain users access their “my mails” page, they get access to
   all mails that share the same thread as mail sent to them, up to the
   timestamp of the mail sent to them.


Server setup
============

All endpoints below the ``restricted`` URL path need HTTP basic authentication,
with the ``Authorization`` header set to a base-64-encoded ``login:password``.
Since these endpoints (currently the “my mails” pages and the “send mail to me”
feature) are not vital, you may ignore that and effectively switch off those
endpoints.

Otherwise, mail2web must reside behind a proxy HTTP server which does the user
authentication and sets the above header.  By limiting this authentication to
``restricted``, you can make using your mail2web instance more convenient.

Since mail2web may take a rather long inital time to walk through all mail
files, there is a ``/healthz`` endpoint that returns HTTP 200 when mail2web is
ready for requests.


Environment
===========

``MAILDIR``
  root directory where the mail folders are expected

``MAIL_FOLDERS``
  comma-separated list of subdirectories of ``MAILDIR`` that contain the mails

``M2W_LOG_PATH``
  Absolute path to the directory where mail2web.log is written to.  If not set,
  ``/tmp`` is used.

``SECRET_KEY_PATH``
  Absolute path to a text file with a secret string which is used e.g. as a
  salt for hashes.  All white space at the beginning and the end of the string
  (inclusing line breaks) is removed.  The default is
  ``/var/lib/mail2web_secrets/secret_key``.

``ROOT_URL``
  URL prefix for all endpoints.  It defaults to the empty string.  If given, it
  must start with a slash and should not end with a slash.


Mail archive structure
======================

The file structure expected by mail2web is::

  MAILDIR
     |
     +---> folder1
     |        |
     |        +---> 1
     |        |
     |        +---> 2
     |        |
     |        +---> 3
     |        |
     |        ⋮
     |
     +---> folder2
     |        |
     ⋮        ⋮

Directories in ``MAILDIR`` not in ``MAIL_FOLDERS`` are ignored, as are files
whose file name does not consist of numbers only.


Configuration file
==================

In ``MAILDIR/permissions.yaml``, you can set the mail addresses of all people
that might log in like so:

.. code-block:: yaml

    addresses:
      username1:
        - user1@example.com
        - functional_mailbox@example.com
        - team_mailbox@example.com
      username2:
        - user2@example.com
        - functional_mailbox@example.com
        - team_mailbox@example.com
      username3:
        - user3@example.com

The respectively first mail address is the primary personal address of that
user, which is used to send mails to them.  The other mail addresses belong to
mail boxes the user can read, too.  They are used to compile the mails for the
user in the “my mails” page.
