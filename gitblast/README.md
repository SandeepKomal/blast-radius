# gitblast

**Change `github` to `gitblast` in any link.**

```
https://github.com/owner/repo/pull/123   →   https://gitblast.com/owner/repo/pull/123
https://github.com/owner/repo            →   https://gitblast.com/owner/repo
```

- **Pull request blast radius** (`/owner/repo/pull/123`). Every changed file is drawn on a map of the whole
  repository, with the files that import it rippling outwards up to three steps. You also get the tests that
  cover the change, what deserves a closer look (deleted files that are still imported, security-sensitive
  code, migrations, exported APIs, CI and dependency changes) and a suggested order for reviewing the files.
- **Repository time-lapse** (`/owner/repo`). The repository's folders animate from the first commit to today,
  with languages, milestones and the commit at each point.

It also accepts a full link pasted after the domain (`gitblast.com/https://github.com/owner/repo`) and has a
search box that takes `owner/repo`, `owner/repo#123` or any GitHub URL.

## How it works

There is no AI and no guessing. For a pull request, gitblast:

1. lists the changed files through the GitHub API;
2. streams the source tarball at the PR's head commit and reads imports from every source file in memory;
3. resolves imports to files for TypeScript/JavaScript (including `@/` aliases and monorepo workspaces), Python,
   Go (through `go.mod`), Rust, Java, Kotlin, Scala, Ruby, PHP, Dart, C and C++;
4. walks outwards from the changed files through everything that imports them.

The time-lapse samples up to 36 evenly spaced commits and reads the file tree at each one, so even repositories
with long histories need only a few dozen API calls.

Static import analysis can't see dynamic imports, reflection or code generation. The report says when a
repository was too large to read in full.

## Running it

Requires Node.js 20.9 or later.

```bash
npm install
cp .env.example .env.local   # optional: add a GITHUB_TOKEN
npm run dev                  # http://localhost:3000
```

Without a token GitHub allows 60 API requests an hour, which is enough to try it. With a fine-grained token that
has **public repositories (read-only)** access and nothing else, the limit is 5,000 an hour.

| Variable | Purpose |
| --- | --- |
| `GITHUB_TOKEN` | Raises the GitHub API limit. Give it no permissions beyond reading public repositories. |
| `GITBLAST_BLOCKLIST` | Comma-separated owners or `owner/repo` pairs that asked not to be shown. |
| `NEXT_PUBLIC_SITE_URL` | The public address, used for canonical links and social cards. Defaults to `https://gitblast.com`. |

```bash
npm test            # unit tests
npm run typecheck
npm run build && npm start
```

### Deploying

It is a standard Next.js app and runs on Vercel, or anywhere Node.js runs (`npm run build && npm start`). API
routes set `Cache-Control` headers so a CDN can serve popular links. The rate limiter and cache live in memory
on each instance; that is fine for one or a few instances. The rate limiter keys on `X-Forwarded-For`, so
run it behind a proxy that sets that header (Vercel and most load balancers do).

## Being a good citizen

- Only public repositories are shown. Private ones are refused even if the token could read them.
- Repository owners can opt out by adding the `gitblast-optout` topic to their repository.
- No cookies, analytics or tracking. Source code is never stored; only the derived report is cached.
- gitblast is not affiliated with GitHub. It uses neither GitHub's logo nor "GitHub" in its name.

See [SECURITY.md](SECURITY.md) for how to report a vulnerability.

## Licence

[MIT](LICENSE)
