# Security policy

Wada is development software. Security fixes target the current `main` branch;
there is no supported stable release series yet.

## Report a vulnerability

Use GitHub's **Security → Report a vulnerability** option when available. If
private reporting is unavailable, open an issue asking for a private reporting
channel without including exploit details, credentials, private source code,
conversation content or personal data. Do not post a vulnerability's technical
details publicly before coordinating with the maintainer. No response-time or
bug-bounty commitment is offered.

## Local data and provider access

Provider configuration, credentials and conversation transcripts live under the
app's data directory. Wada's own settings and records live beside them in SQLite.
These files are sensitive: do not attach the whole data directory to a report.
See the README for the exact locations and how to use temporary data.

Messages, attachments and tool results sent to a model are handled by the chosen
provider. Review that provider's privacy and retention terms. The repository does
not supply a shared provider key or an account. Browser-based provider login and
platform permissions use the selected provider and operating-system facilities.

**Ask before write** permits reads and searches and asks about edits, shell
commands, web access and unknown tools. **Full access** allows broader tool use.
These modes are permission policy, not an operating-system sandbox. Agents work
with your account's filesystem access. Use a disposable checkout for untrusted
projects, inspect changes, and avoid granting access to unrelated sensitive files.
The design canvas displays local HTML in an embedded WebView; treat HTML and
remote resources it loads as untrusted content.

## Maintainer checks

Run `make audit` before a release, review dependency changes, and scan the source
and publishable Git history for secrets. A clean scan is evidence from known
rules and advisories, not a guarantee that every vulnerability is absent.
Never commit provider credentials or code-signing/update keys.
