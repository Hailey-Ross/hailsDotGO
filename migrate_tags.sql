CREATE TABLE IF NOT EXISTS tags (
  id    INT UNSIGNED NOT NULL AUTO_INCREMENT,
  name  VARCHAR(32)  NOT NULL,
  color VARCHAR(7)   NOT NULL DEFAULT '#cccccc',
  PRIMARY KEY (id),
  UNIQUE KEY uk_tag_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_tags (
  user_id INT UNSIGNED NOT NULL,
  tag_id  INT UNSIGNED NOT NULL,
  PRIMARY KEY (user_id, tag_id),
  CONSTRAINT fk_ut_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
  CONSTRAINT fk_ut_tag  FOREIGN KEY (tag_id)  REFERENCES tags (id)  ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO tags (name, color) VALUES ('Dev', '#ec4899');

-- Optional: assign the Dev tag to your superadmin account.
-- Replace 'yourusername' with your actual SUPERADMIN_USER value.
-- INSERT IGNORE INTO user_tags (user_id, tag_id)
--   SELECT u.id, t.id FROM users u JOIN tags t ON t.name = 'Dev'
--   WHERE u.username = 'yourusername';
