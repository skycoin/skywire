package shell

// jq via gojq — a pure Go implementation of the jq query language.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/interp"
	"github.com/itchyny/gojq"
)

func init() {
	applets["jq"] = applet{"JSON processor (gojq; -r raw output, -c compact)", runJq}
}

func runJq(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	flags, rest := parseFlags(args)
	if len(rest) == 0 {
		fprintln(hc.Stderr, "usage: jq [-r] [-c] 'filter' [file...]")
		return 2
	}
	query, err := gojq.Parse(rest[0])
	if err != nil {
		return fail(hc, "jq", err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		return fail(hc, "jq", err)
	}

	input := hc.Stdin
	if len(rest) > 1 {
		data, err := afero.ReadFile(s.FS, resolveArg(hc, rest[1]))
		if err != nil {
			return fail(hc, "jq", err)
		}
		input = bytes.NewReader(data)
	}

	dec := json.NewDecoder(input)
	status := 0
	for {
		var doc any
		if err := dec.Decode(&doc); err != nil {
			if err == io.EOF {
				break
			}
			return fail(hc, "jq", err)
		}
		iter := code.RunWithContext(ctx, doc)
		for {
			v, ok := iter.Next()
			if !ok {
				break
			}
			if err, isErr := v.(error); isErr {
				fprintf(hc.Stderr, "jq: %v\n", err)
				status = 5
				continue
			}
			if str, isStr := v.(string); isStr && flags['r'] {
				fprintln(hc.Stdout, str)
				continue
			}
			var out []byte
			if flags['c'] {
				out, err = json.Marshal(v)
			} else {
				out, err = json.MarshalIndent(v, "", "  ")
			}
			if err != nil {
				fprintf(hc.Stderr, "jq: %v\n", err)
				status = 5
				continue
			}
			fprintln(hc.Stdout, string(out))
		}
	}
	return status
}
