CREATE TABLE item (
	guid       TEXT NOT NULL UNIQUE,
	feed       TEXT NOT NULL,
	title      TEXT NOT NULL,
	link       TEXT NOT NULL,
	judgement  BOOLEAN NULL
);
