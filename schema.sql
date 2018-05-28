CREATE TABLE item (
	guid       TEXT NOT NULL PRIMARY KEY,
	judgement  BOOLEAN NULL,
	score      FLOAT NOT NULL,
	feed       TEXT NOT NULL,
	title      TEXT NOT NULL,
	link       TEXT NOT NULL,
	INDEX judgement_idx (judgement),
	INDEX score_idx (score)
);
