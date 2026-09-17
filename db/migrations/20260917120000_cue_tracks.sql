-- +goose Up
ALTER TABLE media_file ADD COLUMN cue_track integer NOT NULL DEFAULT 0;
ALTER TABLE media_file ADD COLUMN cue_start_sample integer NOT NULL DEFAULT 0;
ALTER TABLE media_file ADD COLUMN cue_end_sample integer NOT NULL DEFAULT 0;
CREATE INDEX media_file_cue_source ON media_file(library_id, path, cue_track);

-- +goose Down
DELETE FROM media_file WHERE cue_track > 0;
DROP INDEX media_file_cue_source;
ALTER TABLE media_file DROP COLUMN cue_end_sample;
ALTER TABLE media_file DROP COLUMN cue_start_sample;
ALTER TABLE media_file DROP COLUMN cue_track;
