Purpose
=======

mail2web exposes an email archive to the Web consisting of folders with
RFC 5322 files.  This way, you can give your mails persistent identifiers while
at the same time you are able to show them – along with their thread and
attachments – to others.

As an example, visit
https://mails.bronger.org/zkoL7KUVtt?tokenOlder=AzE6Xw8ETg.


Security
========

Mails may be very sensitive, so security in mail2web is crucial.  This is
achieved by addressing all mail using a peppered hash like this::

  https://mymails.example.com/87g46e5i78

The hash is base64-URL-encoded with 10 characters, so it has :math:`2^{60}`
possible values.  If you have e.g. :math:`2^{17}` ≈ 130,000 mails, attackers
will have a hit each 8,796,093,022,208 tries, on average.

Let us call the mail addressed by this link the *origin mail*.  Depending on a
``token…`` parameter in the query string of the URL, the link also exposes the
thread associated with the origin mail to everyone that has that link.

There are four access modes:

single
  shows only one single mail, denoted by the hash.

direct
  shows the mail with all its direct ancestors.  In other words, a single
  direct line in the thread to the mail.

older
  shows the mail plus all other mails in the thread that are older than it.
  This is like “direct” but with side branches in the thread.

full
  shows the full thread, including mails that are yet to come to it.


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
  pepper for hashes.  All white space at the beginning and the end of the
  string (inclusing line breaks) is removed.  The default is
  ``/var/lib/mail2web_secrets/secret_key``.

``ROOT_URL``
  URL prefix for all endpoints.  It defaults to the empty string.  If given, it
  must start with a slash and should not end with a slash.

``M2W_SMTP_HOST``
  Host and port of the SMTP host for message submission,
  e.g. ``postfix.local:587``.

``M2W_SMTP_ENVELOPE_SENDER``
  Content of the sender field in the *envelope* of the mail.  Note that the
  ``From:`` field of mails sent by mail2web always have the original ``From:``
  field content, and that you MTA must be okay with that (most aren’t).


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

    admin: username1

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

The user name set in ``admin`` must point to a user name in ``addresses`` with
at least one mail address.  Otherwise, requesting mails in the “my mails” page
does not work.


Getting the URLs
================

In order to get the URL to a mail as the owner of the mails, call
``mail2url.py`` and pass the path to the respective mail file.  The scripts
uses the environment variables ``ROOT_URL`` and ``SECRET_KEY_PATH``.
Additionally, it needs ``DOMAIN`` to be set to e.g. “mails.example.com”.  For
further information, call ``mail2url.py --help``.
