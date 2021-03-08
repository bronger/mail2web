<!DOCTYPE html>
<html lang="en">
<head>
<title>Your mails sent</title>
</head>
<body>
<h1>Your mails sent</h1>

<p>All your mails of the last 30 days were sent to {{.address}} as a CSV file.</p>
<p>This includes mails where the mail address(es) {{.addresses}} occur(s) in
  “<samp>From:</samp>”, “<samp>To:</samp>”, “<samp>Cc:</samp>”, or
  “<samp>Bcc:</samp>”.</p>

<table>
  <thead>
    <tr><th>date</th><th>from</th><th>subject</th><th>message ID</th><th>hash</th></tr>
  </thead>
  <tbody>
    {{range .rows}}
    <tr><td>{{.Timestamp}}</td><td>{{.From}}</td><td>{{.Subject}}</td><td>{{.MessageId}}</td><td>{{.HashId}}</td></tr>
    {{end}}
  </tbody>
</table>
</body>
</html>
