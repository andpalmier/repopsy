package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// catFileBatch streams the objects named by ids from one "git cat-file --batch"
// process, calling fn with each object's index and content in order. A missing
// object is an error.
func (r *Repository) catFileBatch(ctx context.Context, ids []string, fn func(i int, content []byte) error) error {
	return r.catFile(ctx, ids, func(i int, content io.Reader, _ int64, found bool) error {
		if !found {
			return fmt.Errorf("object %s is missing", ids[i])
		}
		data, err := io.ReadAll(content)
		if err != nil {
			return fmt.Errorf("failed to read object %s: %w", ids[i], err)
		}
		return fn(i, data)
	})
}

// ReadObjects reads the objects named by names, which may be any name
// cat-file accepts, such as "<commit>:<path>". fn is called for every name in
// order, with found false when it names nothing. Names containing a newline
// cannot be requested and are reported as not found.
func (r *Repository) ReadObjects(ctx context.Context, names []string, fn func(i int, content []byte, found bool)) error {
	var ask []string
	var idx []int
	for i, n := range names {
		if strings.ContainsAny(n, "\n\r") {
			fn(i, nil, false)
			continue
		}
		ask = append(ask, n)
		idx = append(idx, i)
	}
	return r.catFile(ctx, ask, func(i int, content io.Reader, _ int64, found bool) error {
		if !found {
			fn(idx[i], nil, false)
			return nil
		}
		data, err := io.ReadAll(content)
		if err != nil {
			return fmt.Errorf("failed to read object %s: %w", ask[i], err)
		}
		fn(idx[i], data, true)
		return nil
	})
}

// catFile is the cat-file --batch protocol, tolerating missing objects. The
// callback receives a bounded reader so callers that write files can stream
// without allocating the whole object first.
func (r *Repository) catFile(ctx context.Context, ids []string, fn func(i int, content io.Reader, size int64, found bool) error) (err error) {
	if len(ids) == 0 {
		return nil
	}

	cmd := r.command(ctx, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to open cat-file stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to open cat-file stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start git cat-file: %w", err)
	}

	// Returning early leaves cat-file mid-write with nobody draining stdout and
	// the request goroutine mid-write to stdin, so both block and Wait never
	// returns. Killing the child releases them. On success stdin has been closed
	// and all output consumed, so cat-file exits on its own and a non-zero exit
	// is worth reporting.
	defer func() {
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return
		}
		if waitErr := cmd.Wait(); waitErr != nil {
			err = fmt.Errorf("git cat-file failed: %w", waitErr)
		}
	}()

	// Requests are written while responses are read, rather than in lockstep:
	// a round trip per blob costs more than the whole extraction. cat-file
	// answers in order, so the reader still knows which entry it is looking at.
	// Neither pipe can deadlock because stdout is drained throughout.
	go func() {
		defer func() { _ = stdin.Close() }()
		w := bufio.NewWriter(stdin)
		for _, id := range ids {
			if _, err := fmt.Fprintln(w, id); err != nil {
				return
			}
		}
		_ = w.Flush()
	}()

	reader := bufio.NewReader(stdout)
	for i, id := range ids {
		content, size, err := readBatchObject(reader)
		found := !errors.Is(err, errMissing)
		if err != nil && found {
			return fmt.Errorf("failed to read %s: %w", id, err)
		}
		if found {
			if err := fn(i, content, size, true); err != nil {
				return err
			}
			if _, err := io.Copy(io.Discard, content); err != nil {
				return fmt.Errorf("failed to read %s: %w", id, err)
			}
			if _, err := reader.Discard(1); err != nil {
				return err
			}
			continue
		}
		if err := fn(i, nil, 0, false); err != nil {
			return err
		}
	}
	return nil
}

// errMissing is cat-file's "<name> missing" response.
var errMissing = errors.New("missing")

// readBatchObject reads one "<sha> <type> <size>\n" header and returns a
// reader bounded to the object body. The caller must consume the body.
func readBatchObject(reader *bufio.Reader) (io.Reader, int64, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, 0, err
	}
	if strings.HasSuffix(header, " missing\n") || strings.HasSuffix(header, " ambiguous\n") {
		return nil, 0, errMissing
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return nil, 0, fmt.Errorf("unexpected cat-file header %q", strings.TrimSpace(header))
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("unexpected cat-file size in %q", strings.TrimSpace(header))
	}
	return io.LimitReader(reader, size), size, nil
}
