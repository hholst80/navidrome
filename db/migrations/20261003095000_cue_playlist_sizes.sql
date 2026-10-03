-- +goose Up
-- Match persistence.playlistDownloadSizeSQL. Preserve playlist content and timestamps.
UPDATE playlist SET size = (
  SELECT coalesce(sum(CASE WHEN cue_track = 0 THEN size
WHEN sample_rate <= 0 OR channels <= 0
  OR (cue_end_sample <= cue_start_sample AND duration <= 0) THEN 0
ELSE
  (CASE WHEN cue_end_sample > cue_start_sample THEN cue_end_sample - cue_start_sample
        ELSE CAST(round(max(duration, 0) * sample_rate) AS INTEGER) END)
  * channels * (coalesce(nullif(bit_depth, 0), 16) / 8)
  + CASE WHEN channels > 2 OR coalesce(bit_depth, 16) > 16 THEN 68 ELSE 44 END
END), 0)
  FROM media_file JOIN playlist_tracks ON playlist_tracks.media_file_id = media_file.id
  WHERE playlist_tracks.playlist_id = playlist.id
)
WHERE EXISTS (
  SELECT 1 FROM playlist_tracks JOIN media_file ON media_file.id = playlist_tracks.media_file_id
  WHERE playlist_tracks.playlist_id = playlist.id AND media_file.cue_track > 0
);

-- +goose Down
-- Derived counters will be recalculated by the running version on playlist updates.
SELECT 1;
