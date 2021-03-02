Purpose
=======

mail2web exposes an email archive to the Web consisting of folders with
RFC 5322 files.  This way, you can give your mails persistent identifiers while
at the same time you are able to show them – along with their thread and
attachments – to others.


Server setup
============

mail2web must reside behind a proxy HTTP server which also does the user
authentication – usually with HTTP basic auth – and passes the
``Authorization`` header with a base-64-encoded ``login:password`` to mail2web.

Since mail2web may take a rather long time to walk through all mail files
initially, there is a ``/healthz`` endpoint that returns HTTP 200 when mail2web
is ready for requests.


Environment
===========

``MAILDIR``
  root directory where the mail folders are expected

``MAIL_FOLDERS``
  comma-separated list of subdirectories of ``MAILDIR`` that contain the mails

``M2W_LOG_PATH``
  Absolute path to the directory where mail2web.log is written to.  If not set,
  ``/tmp`` is used.


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


Access permissions
==================

A Go plugin with the name ``permissions.so`` must be in the current working
directory to be loaded by mail2web at run time.  It must contain at least one
public function with the following signature::

  func IsAllowed(loginName, folder, id string, threadRoot string) (allowed bool)

It returns ``true`` if the user with the HTTP basic auth login name
``loginName`` should have access to the mail with the given folder and ID, or
to all mails of the thread with the given ``threadRoot``.  The latter usually
is a so-called link which has the form ``folder/id``.  In case of a fake
root [1]_, it is the message ID (without the angle brackets).

.. [1] This is a thread root mail that is not part of the archive but
   references by other mails.  This can happen, for example, if you were
   included into a discussion in Cc not right from the beginning.
