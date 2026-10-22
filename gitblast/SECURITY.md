# Security

Please report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on this repository rather than in a public issue. You'll get a reply within a few days.

## How gitblast protects itself and the repositories it reads

- **Public repositories only.** Every analysis loads the repository first and refuses it unless GitHub reports
  it as public, even if the server's token could read more.
- **Read-only.** gitblast never writes to GitHub. The token it needs has no permissions beyond reading public data.
- **Untrusted input is validated before use.** Owner and repository names must match GitHub's naming rules
  before they reach a URL, so a request can't be pointed at another host.
- **Bounded work.** Source downloads stop at 150 MB compressed, 700 MB unpacked and 150,000 entries. Requests
  time out, the per-client rate limit caps analyses, and identical requests share one analysis.
- **No code leaves the server and none is kept.** Source is held in memory during one analysis. Only the derived
  report is cached.
- **Strict browser headers.** A content security policy limits scripts, images and connections to gitblast
  itself (plus GitHub avatars), and framing is denied.
