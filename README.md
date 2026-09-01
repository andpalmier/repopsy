# repopsy

<p align="center">
    <a href="https://github.com/andpalmier/repopsy/blob/main/LICENSE"><img alt="Software License" src="https://img.shields.io/badge/License-AGPL--3.0-blue.svg"></a>
    <a href="https://godoc.org/github.com/andpalmier/repopsy"><img alt="GoDoc Card" src="https://godoc.org/github.com/andpalmier/repopsy?status.svg"></a>
    <a href="https://goreportcard.com/report/github.com/andpalmier/repopsy"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/andpalmier/repopsy?style=flat-square"></a>
    <a href="https://x.com/intent/follow?screen_name=andpalmier"><img src="https://img.shields.io/twitter/follow/andpalmier?style=social&logo=x" alt="follow on X"></a>
</p>

<p align="center">
  <img src="repopsy_demo.gif" alt="Repopsy Demo">
</p>

**Repopsy** stands for **Rep**ository aut**opsy**.

**Repopsy** is an OSINT tool to gather information on a git repository, it takes a git repo and *"explodes it"*: creating a snapshot folder for every commit, enabling easy comparison, analysis, and archival of code evolution.

How It Works:

1. Resolves the path to its repository root and validates it
2. Lists commits, gathering every metadata field in a single `git log`
3. Creates worker goroutines
4. Each worker uses `git archive | tar -x` for efficient extraction
5. Writes metadata to each folder in `COMMIT_INFO.txt`

Any path inside a repository names the repository itself, so `repopsy .` behaves
the same from any subdirectory. Bare repositories are supported.

Requires `git` 2.31 or newer.

## Installation

### With Homebrew

```bash
brew install andpalmier/tap/repopsy
```

### With Go

```bash
go install github.com/andpalmier/repopsy@latest
```

### Pre-built Binaries

Download pre-built binaries from the [Releases](https://github.com/andpalmier/repopsy/releases) page:

**Linux:**

```bash
curl -LO https://github.com/andpalmier/repopsy/releases/latest/download/repopsy_linux_amd64.tar.gz
tar -xzf repopsy_linux_amd64.tar.gz
sudo mv repopsy /usr/local/bin/
```

**macOS:**

```bash
curl -LO https://github.com/andpalmier/repopsy/releases/latest/download/repopsy_darwin_arm64.tar.gz
tar -xzf repopsy_darwin_arm64.tar.gz
sudo mv repopsy /usr/local/bin/
```

### Docker

```bash
docker pull ghcr.io/andpalmier/repopsy:latest
docker run --rm -v "$(pwd):/repo" ghcr.io/andpalmier/repopsy:latest /repo
```

### From Source

```bash
git clone https://github.com/andpalmier/repopsy.git
cd repopsy
go build -o repopsy .
```

## Usage

```bash
repopsy [flags] <repository-path>
```

### Basic execution

```bash
repopsy .
```

### Options

| Flag | Description | Default |
|------|-------------|---------|
| `-o`, `--output` | Output directory | `./<repo-name>-exploded` |
| `-w`, `--workers` | Number of parallel workers, per branch (max 32) | CPUs, capped at 32 |
| `-n`, `--limit` | Maximum number of commits to extract | 0 (all) |
| `-b`, `--branch` | Branch to extract from | all local branches |
| `-v`, `--verbose` | Show detailed output per commit | false |
| `-h`, `--help` | Show help message | false |
| `--version` | Show version information | false |

### Examples

Extract last 5 commits:

```bash
repopsy -n 5 /path/to/repo
```

Extract from a specific branch:

```bash
repopsy -b main /path/to/repo
```

Verbose output:

```bash
repopsy -v .
```

Extract with 8 workers:

```bash
repopsy -w 8 /path/to/repo
```

## Output Structure

When extracting all branches, each branch name becomes a directory path. A
branch containing `/` nests, mirroring its ref path:

```
<repo>-exploded/
├── main/
│   ├── 20231205_143022_abc1234/
│   │   ├── COMMIT_INFO.txt
│   │   └── ... (source files)
│   └── 20231205_150000_def5678/
├── feature/
│   └── login/                    <- branch "feature/login"
│       └── ...
└── develop/
    └── ...
```

Nesting rather than flattening `/` to `_` means `feature/login` and
`feature_login` stay distinct. Git already refuses to create a branch `main/x`
while `main` exists, so no two branches can claim the same directory.

When extracting a single branch:
```
<repo>-exploded/
├── 20231205_143022_abc1234/
│   ├── COMMIT_INFO.txt
│   └── ... (source files)
└── 20231205_150000_def5678/
```

## Known Limitations

- **Branch names and Windows paths.** Some ref names are legal in git but cannot
  be directory names on Windows: reserved device names (`aux`, `CON`, `NUL`,
  `COM1`…) and the characters `<`, `>`, `"`, `|`. Extracting all branches from a
  repository containing such a branch will fail on Windows. Linux and macOS are
  unaffected.
- **"All branches" means local branches.** Only `refs/heads/` is listed;
  remote-tracking refs and tags are not extracted. On a fresh clone that is
  usually a single branch — fetch or check out the branches you want first, or
  pass `-b`.

## Commit Metadata

Each exploded folder includes a `COMMIT_INFO.txt` file containing metadata about
the commit: this includes verification status (GPG), timestamps, and authorship
details.

Change statistics are measured against the commit's first parent. A merge commit
is measured the same way, so changes brought in from the other side of the merge
are not counted twice.

**Example `COMMIT_INFO.txt` content:**

```text
COMMIT INFORMATION
===========================

Hash:           8f6a2b1c4d5e...
Short Hash:     8f6a2b1

AUTHOR (who wrote the code)
---------------------------
Name:           Alice Dev
Email:          alice@example.com
Date:           2023-12-05T14:30:22Z
Timestamp:      1701786622

COMMITTER (who applied the commit)
----------------------------------
Name:           Bob Ops
Email:          bob@example.com
Date:           2023-12-05T15:00:00Z
Timestamp:      1701788400

VERIFICATION
------------
GPG Signature:  Valid signature (good)

LINEAGE
-------
Parents:        7e5d1c2b... 

CHANGE STATISTICS
-----------------
Files Changed:  5
Insertions:     +120
Deletions:      -34

COMMIT MESSAGE
--------------
Subject:
Fix critical security vulnerability in extraction logic

Full Message:
Fix critical security vulnerability in extraction logic

This patch addresses CVE-2023-XXXX by sanitizing input paths...
```