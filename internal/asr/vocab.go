package asr

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// wordStartMarker is the SentencePiece marker (U+2581 LOWER ONE EIGHTH
// BLOCK) prefixed to the first token of each word by the Parakeet
// tokenizer.
const wordStartMarker = "▁"

// loadVocab reads a "<token> <id>" per line vocabulary file into a slice
// indexed by id.
func loadVocab(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseVocab(f)
}

func parseVocab(r io.Reader) ([]string, error) {
	ids := map[int]string{}
	maxID := -1

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		sp := strings.LastIndex(line, " ")
		if sp < 0 {
			return nil, fmt.Errorf("asr: malformed vocab line %q", line)
		}
		tok, idStr := line[:sp], line[sp+1:]
		id, err := strconv.Atoi(idStr)
		if err != nil {
			return nil, fmt.Errorf("asr: bad id in vocab line %q: %w", line, err)
		}
		ids[id] = tok
		if id > maxID {
			maxID = id
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	tokens := make([]string, maxID+1)
	for id, tok := range ids {
		tokens[id] = tok
	}
	return tokens, nil
}

// decodeTokens joins token ids into text, converting the SentencePiece
// word-start marker into spaces. Unknown ids are skipped.
func decodeTokens(ids []int32, vocab []string) string {
	var sb strings.Builder
	for _, id := range ids {
		if int(id) < 0 || int(id) >= len(vocab) {
			continue
		}
		sb.WriteString(vocab[id])
	}
	return strings.TrimSpace(strings.ReplaceAll(sb.String(), wordStartMarker, " "))
}
