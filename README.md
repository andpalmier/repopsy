# gitxplode 🔮

**Expand git repositories by extracting each commit into a separate folder.**

gitxplode takes a git repository and creates a snapshot folder for every commit, enabling easy comparison, analysis, or archival of code evolution.

## Features

- ⚡ **Fast** - Parallel extraction using configurable worker pools
- 📦 **Zero Dependencies** - Uses git CLI, no external Go libraries required
- 🎨 **Flexible Naming** - Choose between hash, date-hash, or index-hash folder formats
- 🖥️ **Nice UX** - Progress bar with ETA, graceful interrupt handling
- 🔧 **Simple** - Clean CLI with intuitive flags

## Installation

### Using Go Install

```bash
go install github.com/andpalmier/gitxplode@latest
```

### From Source

```bash
git clone https://github.com/andpalmier/gitxplode.git
cd gitxplode
go build -o gitxplode .
```

## Requirements

- Go 1.21+ (for building)
- Git (must be available in PATH)

## Usage

```bash
gitxplode [flags] <repository-path>
```

### Examples

```bash
# Extract all commits from current directory
gitxplode .

# Extract last 10 commits to custom output directory
gitxplode -n 10 -o ./versions /path/to/repo

# Extract with date-prefixed folders using 4 workers
gitxplode -f date-hash -w 4 /path/to/repo

# Quiet mode (no progress output)
gitxplode -q .

# Verbose mode (show each commit as it's extracted)
gitxplode -v .
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--output` | `-o` | `<repo>-exploded` | Output directory |
| `--workers` | `-w` | CPU count | Number of parallel workers |
| `--limit` | `-n` | 0 (all) | Maximum commits to extract |
| `--branch` | `-b` | HEAD | Branch to extract from |
| `--format` | `-f` | `hash` | Folder naming format |
| `--quiet` | `-q` | false | Suppress progress output |
| `--verbose` | `-v` | false | Show per-commit details |
| `--version` | | | Show version information |
| `--help` | `-h` | | Show help message |

### Folder Naming Formats

| Format | Example | Description |
|--------|---------|-------------|
| `hash` | `abc1234` | Short commit hash |
| `date-hash` | `2024-01-15_abc1234` | Date prefix with hash |
| `index-hash` | `001_abc1234` | Sequential index with hash |

## Output Structure

```
myrepo-exploded/
├── abc1234/           # Oldest commit
│   ├── main.go
│   └── ...
├── def5678/           # Second commit
│   ├── main.go
│   ├── utils.go
│   └── ...
└── ghi9012/           # Newest commit
    ├── main.go
    ├── utils.go
    ├── README.md
    └── ...
```

## Use Cases

- **Code Review** - Compare file changes across multiple commits side-by-side
- **Static Analysis** - Run analysis tools on historical versions
- **Archival** - Create standalone snapshots of repository states
- **Debugging** - Quickly access code at specific points in history
- **Documentation** - Generate visual diffs for documentation purposes

## How It Works

1. Opens the git repository and validates it
2. Lists commits (optionally filtered by branch/limit)
3. Spawns a pool of worker goroutines
4. Each worker uses `git archive | tar -x` to extract commit contents
5. Progress is reported in real-time with ETA

The extraction uses `git archive` piped to `tar`, which is efficient and doesn't require worktree manipulation or temporary checkouts.

## Performance Tips

- **Use SSDs** - Extraction is I/O bound, SSDs significantly improve performance
- **Adjust Workers** - For large files, fewer workers may be faster; for many small files, more workers help
- **Limit Commits** - Use `-n` to extract only recent commits if you don't need full history

## License

MIT License - see [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
