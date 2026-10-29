package highlight

import "testing"

func TestIsDuplicateHighlightGame(t *testing.T) {
	prev := []string{
		"The Legend of Zelda: Ocarina of Time",
		"Super Mario Galaxy",
		"Metroid Prime",
	}

	if !isDuplicateHighlightGame("Zelda: Ocarina of Time", prev) {
		t.Fatalf("expected 'Zelda: Ocarina of Time' to match 'The Legend of Zelda: Ocarina of Time'")
	}
	if !isDuplicateHighlightGame("Ocarina of Time", prev) {
		t.Fatalf("expected 'Ocarina of Time' to match subtitle")
	}
	if !isDuplicateHighlightGame("Metroid Prime (GameCube, 2002)", prev) {
		t.Fatalf("expected parenthetical candidate to match 'Metroid Prime'")
	}
	if isDuplicateHighlightGame("Super Mario Galaxy 2 (Wii, 2010)", prev) {
		t.Fatalf("did not expect 'Super Mario Galaxy 2' to be marked duplicate of 'Super Mario Galaxy'")
	}

	candidates := pickUnusedCandidates(prev, 5)
	if len(candidates) != 5 {
		t.Fatalf("expected 5 unused candidates, got %d", len(candidates))
	}
	for _, c := range candidates {
		if isDuplicateHighlightGame(c, prev) {
			t.Fatalf("picked candidate %q which is in previousGames", c)
		}
	}
}

