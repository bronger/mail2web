<!DOCTYPE html>
<html lang="en">
<head>
<title>Your mails</title>
<style>
  table {border: 1px solid}
  td, th {border: 1px solid}
</style>
</head>
<body>
<h1>Your mails</h1>

<p>The following table shows all your mails of the last 30 days.</p>
<p>This includes mails where the mail address(es) {{.addresses}} occur(s) in
  “<samp>From:</samp>”, “<samp>To:</samp>”, “<samp>Cc:</samp>”, or
  “<samp>Bcc:</samp>”.</p>

<table>
  <thead>
    <tr><th>date</th><th>from</th><th>subject</th><th>message ID</th></tr>
  </thead>
  <tbody>
    {{range .rows}}
    <tr>
      <td><a href="{{.FullThreadLink}}">{{.Timestamp}}</a></td>
      <td>{{.From}}</td>
      <td>{{.Subject}}</td>
      <td style="overflow-wrap: break-word; max-width: 20em">{{.MessageID}}</td>
    </tr>
    {{end}}
  </tbody>
</table>
</body>
</html>
