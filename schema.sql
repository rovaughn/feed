CREATE TABLE item (
	guid       TEXT NOT NULL UNIQUE,
	loaded     INTEGER NOT NULL,
	feed       TEXT NOT NULL,
	title      TEXT NOT NULL,
	link       TEXT NOT NULL,
	judgement  BOOLEAN NULL
);

CREATE INDEX item_loaded ON item(loaded);
