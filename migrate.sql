-- hailsdotgo migrate.sql -- upgrade path for existing installs
-- New installs: run schema.sql only. Do not run this file on a fresh database.
--
-- RECOMMENDED: let the migrate tool apply the right sections for you:
--   go run ./cmd/migrate -from <your version>   (first time, e.g. -from v0.1.4a)
--   go run ./cmd/migrate                         (every upgrade after that)
--   go run ./cmd/migrate -status                 (see applied vs pending)
-- The tool tracks applied sections in a schema_migrations table and only runs
-- what is pending, tolerating sections that were already applied by hand.
--
-- MANUAL fallback: apply each numbered section you have not yet run, in order.
-- CREATE TABLE IF NOT EXISTS and INSERT IGNORE lines are safe to re-run.
-- ALTER TABLE and MODIFY COLUMN lines will error if already applied --
-- skip them once done, or check your schema first.
--
-- Section headers MUST stay `-- N. Title` with contiguous numbering: the tool
-- parses them. To add a migration, append the next `-- N. ...` section here and
-- mirror it in schema.sql, then run `go run ./cmd/migrate -dump-seed` to refresh
-- the schema_migrations seed block in schema.sql.

-- 1. Role expansion + disabled flag
ALTER TABLE users MODIFY COLUMN role ENUM('user','moderator','admin') NOT NULL DEFAULT 'user';
ALTER TABLE users MODIFY COLUMN role ENUM('user','tester','moderator','admin') NOT NULL DEFAULT 'user';
ALTER TABLE users ADD COLUMN disabled TINYINT(1) NOT NULL DEFAULT 0 AFTER role;

-- 2. Trainer profile
ALTER TABLE users ADD COLUMN trainer_name   VARCHAR(64)  NOT NULL DEFAULT '' AFTER disabled;
ALTER TABLE users ADD COLUMN trainer_code   VARCHAR(16)  NOT NULL DEFAULT '' AFTER trainer_name;
ALTER TABLE users ADD COLUMN avatar         VARCHAR(32)  NOT NULL DEFAULT '' AFTER trainer_code;
ALTER TABLE users ADD COLUMN city           VARCHAR(100) NOT NULL DEFAULT '' AFTER avatar;
ALTER TABLE users ADD COLUMN profile_public TINYINT(1)   NOT NULL DEFAULT 0  AFTER city;

-- 3. Granular location fields
ALTER TABLE users ADD COLUMN region           VARCHAR(100) NOT NULL DEFAULT ''     AFTER city;
ALTER TABLE users ADD COLUMN country          VARCHAR(100) NOT NULL DEFAULT ''     AFTER region;
ALTER TABLE users ADD COLUMN location_display ENUM('none','country','full') NOT NULL DEFAULT 'none' AFTER country;

-- 4. Pronouns
ALTER TABLE users ADD COLUMN pronouns VARCHAR(32) NOT NULL DEFAULT '' AFTER avatar;

-- 5. Directory hidden flag
ALTER TABLE users ADD COLUMN directory_hidden TINYINT(1) NOT NULL DEFAULT 0 AFTER profile_public;

-- 6. Raid ban flag
ALTER TABLE users ADD COLUMN raid_banned TINYINT(1) NOT NULL DEFAULT 0 AFTER directory_hidden;

-- 7. Favourite Pokemon
ALTER TABLE users ADD COLUMN fav_pokemon      VARCHAR(64)  NOT NULL DEFAULT '' AFTER raid_banned;
ALTER TABLE users ADD COLUMN fav_pokemon_form VARCHAR(32)  NOT NULL DEFAULT '' AFTER fav_pokemon;
ALTER TABLE users ADD COLUMN fav_sprite_url   VARCHAR(255) NOT NULL DEFAULT '' AFTER fav_pokemon_form;

-- 8. User strikes
CREATE TABLE IF NOT EXISTS user_strikes (
  id             INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        INT UNSIGNED NOT NULL,
  reason         VARCHAR(255) NOT NULL DEFAULT '',
  issued_by      INT UNSIGNED NOT NULL,
  issued_by_name VARCHAR(32)  NOT NULL DEFAULT '',
  created_at     DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_strike_user (user_id),
  CONSTRAINT fk_strike_user   FOREIGN KEY (user_id)   REFERENCES users (id) ON DELETE CASCADE,
  CONSTRAINT fk_strike_issuer FOREIGN KEY (issued_by) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 9. Legacy raid finder tables (v1)
CREATE TABLE IF NOT EXISTS raid_posts (
  id              INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id         INT UNSIGNED NOT NULL,
  boss_name       VARCHAR(64)  NOT NULL,
  note            VARCHAR(160) NOT NULL DEFAULT '',
  players_needed  TINYINT UNSIGNED NOT NULL DEFAULT 0,
  weather_boosted TINYINT(1)   NOT NULL DEFAULT 0,
  created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at      DATETIME     NOT NULL,
  PRIMARY KEY (id),
  KEY idx_raid_expires (expires_at),
  CONSTRAINT fk_raidpost_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS raid_joins (
  id           INT UNSIGNED NOT NULL AUTO_INCREMENT,
  post_id      INT UNSIGNED NOT NULL,
  joiner_id    INT UNSIGNED NOT NULL,
  confirmed    TINYINT(1)   NOT NULL DEFAULT 0,
  host_invited TINYINT(1)   NOT NULL DEFAULT 0,
  joined_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_join (post_id, joiner_id),
  CONSTRAINT fk_join_post FOREIGN KEY (post_id)   REFERENCES raid_posts (id) ON DELETE CASCADE,
  CONSTRAINT fk_join_user FOREIGN KEY (joiner_id) REFERENCES users (id)      ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS raid_leave_log (
  id       INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id  INT UNSIGNED NOT NULL,
  left_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_leave_user_time (user_id, left_at),
  CONSTRAINT fk_leave_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS raid_join_cooldowns (
  user_id INT UNSIGNED NOT NULL,
  until   DATETIME     NOT NULL,
  PRIMARY KEY (user_id),
  CONSTRAINT fk_cooldown_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS raid_ratings (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  post_id    INT UNSIGNED NOT NULL,
  rater_id   INT UNSIGNED NOT NULL,
  rated_id   INT UNSIGNED NOT NULL,
  score      TINYINT UNSIGNED NOT NULL,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_rating (post_id, rater_id, rated_id),
  CONSTRAINT fk_rating_rater FOREIGN KEY (rater_id) REFERENCES users (id) ON DELETE CASCADE,
  CONSTRAINT fk_rating_rated FOREIGN KEY (rated_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 10. raid_joins status column
-- Default 'accepted' preserves existing open-join rows created before this migration.
ALTER TABLE raid_joins ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'accepted' AFTER host_invited;

-- 11. Invite token length (supports both old 64-char and new shorter codes)
ALTER TABLE invites MODIFY COLUMN token VARCHAR(64) NOT NULL;

-- 12. Invite role assignment, multi-use codes, pending role confirmation
ALTER TABLE invites ADD COLUMN granted_role ENUM('user','tester','moderator','admin') NOT NULL DEFAULT 'user' AFTER created_by;
ALTER TABLE invites ADD COLUMN max_uses  TINYINT UNSIGNED NOT NULL DEFAULT 1 AFTER expires_at;
ALTER TABLE invites ADD COLUMN use_count TINYINT UNSIGNED NOT NULL DEFAULT 0 AFTER max_uses;
UPDATE invites SET use_count = 1 WHERE used_at IS NOT NULL;
ALTER TABLE users ADD COLUMN pending_role ENUM('tester','moderator','admin') NULL DEFAULT NULL AFTER role;

-- 13. API access permission
ALTER TABLE users ADD COLUMN api_access TINYINT(1) NOT NULL DEFAULT 0 AFTER role;

-- 14. Tag system
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

-- 15. Raid XP and activity tracking
ALTER TABLE users ADD COLUMN raid_xp      INT UNSIGNED  NOT NULL DEFAULT 0     AFTER raid_banned;
ALTER TABLE users ADD COLUMN last_raid_at DATETIME      NULL                    AFTER raid_xp;
ALTER TABLE users ADD COLUMN rater_weight DECIMAL(4,3)  NOT NULL DEFAULT 1.000  AFTER last_raid_at;
ALTER TABLE users ADD COLUMN last_seen_at DATETIME      NULL                    AFTER rater_weight;

-- 16. User language preference
ALTER TABLE users ADD COLUMN lang VARCHAR(8) NOT NULL DEFAULT 'en' AFTER email;

-- 17. Store system
CREATE TABLE IF NOT EXISTS store_items (
  id          INT UNSIGNED NOT NULL AUTO_INCREMENT,
  name        VARCHAR(64)  NOT NULL,
  slug        VARCHAR(32)  NOT NULL,
  description TEXT         NOT NULL,
  price_cents INT UNSIGNED NOT NULL,
  type        ENUM('one_time','monthly','bimonthly') NOT NULL DEFAULT 'one_time',
  benefit     VARCHAR(32)  NOT NULL DEFAULT '',
  active      TINYINT(1)   NOT NULL DEFAULT 1,
  sort_order  TINYINT UNSIGNED NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  UNIQUE KEY uk_slug (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS purchases (
  id              INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id         INT UNSIGNED NOT NULL,
  item_id         INT UNSIGNED NOT NULL,
  paypal_order_id VARCHAR(64)  NOT NULL DEFAULT '',
  status          ENUM('pending','completed','refunded','cancelled') NOT NULL DEFAULT 'pending',
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  completed_at    DATETIME NULL,
  expires_at      DATETIME NULL,
  PRIMARY KEY (id),
  KEY idx_purchase_user (user_id),
  CONSTRAINT fk_purchase_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE,
  CONSTRAINT fk_purchase_item FOREIGN KEY (item_id) REFERENCES store_items (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS custom_tag_requests (
  id            INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id       INT UNSIGNED NOT NULL,
  name          VARCHAR(32)  NOT NULL,
  color         VARCHAR(7)   NOT NULL DEFAULT '#cccccc',
  status        ENUM('pending','approved','rejected') NOT NULL DEFAULT 'pending',
  reviewed_by   INT UNSIGNED NULL,
  reviewed_at   DATETIME NULL,
  reject_reason VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_ctr_status (status),
  CONSTRAINT fk_ctr_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE custom_tag_requests
  MODIFY COLUMN status ENUM('pending','approved','rejected','revision') NOT NULL DEFAULT 'pending';

ALTER TABLE raid_joins ADD COLUMN priority TINYINT(1) NOT NULL DEFAULT 0 AFTER status;

INSERT IGNORE INTO site_settings (setting_key, setting_value) VALUES ('store_enabled', '0');

INSERT IGNORE INTO store_items (name, slug, description, price_cents, type, benefit, sort_order) VALUES
  ('Supporter Pack', 'supporter',
   'Submit a custom tag (staff-reviewed) that appears on your trainer profile. Includes a Supporter badge.',
   299, 'bimonthly', 'supporter', 1),
  ('Priority Pass', 'priority_pass',
   'Jump to the front of the raid join queue for 30 days.',
   799, 'monthly', 'queue_priority', 2);

-- 18. Boss tier on raid posts
ALTER TABLE raid_posts ADD COLUMN boss_tier TINYINT UNSIGNED NOT NULL DEFAULT 0 AFTER boss_name;

-- 19. Custom tag cooldown + color rate limiting
ALTER TABLE users ADD COLUMN tag_requested_at DATETIME NULL AFTER last_seen_at;

CREATE TABLE IF NOT EXISTS tag_color_changes (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    INT UNSIGNED NOT NULL,
  changed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_tcc_user_time (user_id, changed_at),
  CONSTRAINT fk_tcc_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 20. Account suspension reason
ALTER TABLE users ADD COLUMN disabled_reason VARCHAR(255) NOT NULL DEFAULT '' AFTER disabled;

-- 21. Translator permission + translation edits
-- t_key is named t_key because "key" is a reserved word in MySQL.
-- old_text snapshots the merged value at submit time so reviewers can see if it changed before approval.
ALTER TABLE users ADD COLUMN translator TINYINT(1) NOT NULL DEFAULT 0 AFTER api_access;

CREATE TABLE IF NOT EXISTS translation_edits (
  id            INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id       INT UNSIGNED NOT NULL,
  lang          CHAR(2)      NOT NULL,
  t_key         VARCHAR(191) NOT NULL,
  old_text      TEXT         NOT NULL,
  new_text      TEXT         NOT NULL,
  status        ENUM('pending','approved','rejected') NOT NULL DEFAULT 'pending',
  reviewed_by   INT UNSIGNED NULL,
  reviewed_at   DATETIME NULL,
  reject_reason VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_te_status (status),
  KEY idx_te_user_lang_key (user_id, lang, t_key),
  CONSTRAINT fk_te_user     FOREIGN KEY (user_id)     REFERENCES users (id) ON DELETE CASCADE,
  CONSTRAINT fk_te_reviewer FOREIGN KEY (reviewed_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 22. Raid Finder v2: lobbies, trust events, awards
-- Replaces the browse-and-join raid post board with per-boss matchmaking queues,
-- host lobbies with server-enforced timers, a behavioral trust system, and a
-- community awards system. Old tables stay for history; v2 stops writing to them.
-- users.rater_weight is repurposed as trust report reliability (same range).
CREATE TABLE IF NOT EXISTS raid_lobbies (
  id              INT UNSIGNED NOT NULL AUTO_INCREMENT,
  host_id         INT UNSIGNED NOT NULL,
  boss_name       VARCHAR(64)  NOT NULL,
  boss_tier       TINYINT UNSIGNED NOT NULL DEFAULT 0,
  is_custom       TINYINT(1)   NOT NULL DEFAULT 0,
  note            VARCHAR(160) NOT NULL DEFAULT '',
  weather_boosted TINYINT(1)   NOT NULL DEFAULT 0,
  max_members     TINYINT UNSIGNED NOT NULL DEFAULT 5,
  state           ENUM('open','full','raiding','reported','cancelled','expired') NOT NULL DEFAULT 'open',
  invite_deadline DATETIME NULL,
  created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at      DATETIME     NOT NULL,
  closed_at       DATETIME NULL,
  PRIMARY KEY (id),
  KEY idx_lobby_state_boss (state, boss_name, created_at),
  KEY idx_lobby_expires (expires_at),
  CONSTRAINT fk_lobby_host FOREIGN KEY (host_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS raid_lobby_members (
  id               INT UNSIGNED NOT NULL AUTO_INCREMENT,
  lobby_id         INT UNSIGNED NOT NULL,
  user_id          INT UNSIGNED NOT NULL,
  state            ENUM('matched','confirmed','timed_out','left','removed','requeued') NOT NULL DEFAULT 'matched',
  priority         TINYINT(1)   NOT NULL DEFAULT 0,
  matched_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  confirm_deadline DATETIME     NOT NULL,
  confirmed_at     DATETIME NULL,
  attended         TINYINT(1)   NULL,
  left_early       TINYINT(1)   NOT NULL DEFAULT 0,
  raid_success     TINYINT(1)   NULL,
  host_vote        ENUM('none','commend','dislike') NOT NULL DEFAULT 'none',
  PRIMARY KEY (id),
  UNIQUE KEY uk_lobby_member (lobby_id, user_id),
  KEY idx_member_user (user_id, state),
  CONSTRAINT fk_lm_lobby FOREIGN KEY (lobby_id) REFERENCES raid_lobbies (id) ON DELETE CASCADE,
  CONSTRAINT fk_lm_user  FOREIGN KEY (user_id)  REFERENCES users (id)        ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- enqueued_at uses millisecond precision; preserved on requeue so seniority is maintained.
CREATE TABLE IF NOT EXISTS raid_queue (
  user_id      INT UNSIGNED NOT NULL,
  boss_name    VARCHAR(64)  NOT NULL,
  boss_tier    TINYINT UNSIGNED NOT NULL DEFAULT 0,
  priority     TINYINT(1)   NOT NULL DEFAULT 0,
  enqueued_at  DATETIME(3)  NOT NULL,
  last_seen_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id),
  KEY idx_queue_boss (boss_name, priority, enqueued_at),
  CONSTRAINT fk_rq_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- actor_id NULL = system event. uk_te_vote makes commend/dislike idempotent per lobby per pair.
-- host_unfulfilled is added in section 24; this creates the table without it first.
CREATE TABLE IF NOT EXISTS trust_events (
  id            INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id       INT UNSIGNED NOT NULL,
  actor_id      INT UNSIGNED NULL,
  lobby_id      INT UNSIGNED NULL,
  event_type    ENUM('commend','dislike','confirm_timeout','invite_window_fail','left_early','raid_success','staff_adjust') NOT NULL,
  raw_delta     DECIMAL(6,2) NOT NULL,
  weight        DECIMAL(4,3) NOT NULL DEFAULT 1.000,
  applied_delta DECIMAL(6,2) NOT NULL,
  note          VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_te_user (user_id, created_at),
  KEY idx_te_actor (actor_id, event_type, created_at),
  UNIQUE KEY uk_te_vote (lobby_id, actor_id, user_id, event_type),
  CONSTRAINT fk_tev_user  FOREIGN KEY (user_id)  REFERENCES users (id) ON DELETE CASCADE,
  CONSTRAINT fk_tev_actor FOREIGN KEY (actor_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE users ADD COLUMN trust_score  DECIMAL(7,2) NOT NULL DEFAULT 0.00 AFTER rater_weight;
ALTER TABLE users ADD COLUMN special_rank ENUM('','trusted','content_creator') NOT NULL DEFAULT '' AFTER trust_score;

CREATE TABLE IF NOT EXISTS awards (
  id          INT UNSIGNED NOT NULL AUTO_INCREMENT,
  slug        VARCHAR(32)  NOT NULL,
  name        VARCHAR(64)  NOT NULL,
  description VARCHAR(255) NOT NULL DEFAULT '',
  icon        VARCHAR(16)  NOT NULL DEFAULT '🏆',
  color       VARCHAR(7)   NOT NULL DEFAULT '#f0b429',
  active      TINYINT(1)   NOT NULL DEFAULT 1,
  sort_order  TINYINT UNSIGNED NOT NULL DEFAULT 0,
  created_by  INT UNSIGNED NULL,
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_award_slug (slug),
  CONSTRAINT fk_award_creator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Same award from different granters stacks; one granter cannot repeat it.
CREATE TABLE IF NOT EXISTS award_grants (
  id           INT UNSIGNED NOT NULL AUTO_INCREMENT,
  award_id     INT UNSIGNED NOT NULL,
  recipient_id INT UNSIGNED NOT NULL,
  granter_id   INT UNSIGNED NULL,
  note         VARCHAR(160) NOT NULL DEFAULT '',
  created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_grant_once (award_id, recipient_id, granter_id),
  KEY idx_grant_recipient (recipient_id),
  CONSTRAINT fk_ag_award     FOREIGN KEY (award_id)     REFERENCES awards (id) ON DELETE CASCADE,
  CONSTRAINT fk_ag_recipient FOREIGN KEY (recipient_id) REFERENCES users (id)  ON DELETE CASCADE,
  CONSTRAINT fk_ag_granter   FOREIGN KEY (granter_id)   REFERENCES users (id)  ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO site_settings (setting_key, setting_value) VALUES
  ('awards_community_grants_enabled', '0'),
  ('awards_grant_min_trust',          '50'),
  ('raid_custom_lobby_min_trust',     '50');

INSERT IGNORE INTO awards (slug, name, description, icon, color, sort_order) VALUES
  ('helping-hand',    'Helping Hand',      'Goes out of their way to help other trainers',              '🤝', '#4caf50', 1),
  ('friend-ball',     'Friend Ball',       'Welcoming and friendly to everyone in lobbies',             '💚', '#7bd389', 2),
  ('professors-aide', 'Professor''s Aide', 'Great teacher: explains counters, mechanics, and strategy', '📚', '#5b9cf6', 3),
  ('ace-trainer',     'Ace Trainer',       'A role model for the community',                            '⭐', '#f0b429', 4),
  ('joyful-spirit',   'Joyful Spirit',     'Keeps raids fun and positive',                              '🎉', '#e91e63', 5);

-- 23. Locale registry
-- Translators can create new locales from /translate; they stay hidden until an admin enables them.
-- en is implicit (always enabled, never editable) and is not stored here.
CREATE TABLE IF NOT EXISTS locales (
  code       CHAR(2)      NOT NULL,
  enabled    TINYINT(1)   NOT NULL DEFAULT 0,
  created_by INT UNSIGNED NULL,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (code),
  CONSTRAINT fk_loc_creator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO locales (code, enabled) VALUES ('es', 1), ('fr', 1), ('de', 1);

-- 24. Host unfulfilled trust event (2026-06-11)
-- RUN THIS ON THE LIVE DB BEFORE DEPLOYING -- inserts of the new event type will fail without it.
-- Hosts whose lobbies end without the raid starting now log a host_unfulfilled trust event.
ALTER TABLE trust_events MODIFY event_type
  ENUM('commend','dislike','confirm_timeout','invite_window_fail','left_early','raid_success','staff_adjust','host_unfulfilled') NOT NULL;

-- 25. Translator applications (2026-06-11)
-- RUN THIS ON THE LIVE DB BEFORE DEPLOYING the translator application feature.
-- languages is a JSON array VARCHAR(500): [{"code":"de","level":"fluent"}, ...].
-- 'accepted' status is informational; the actual gate is users.translator = 1.
CREATE TABLE IF NOT EXISTS translator_applications (
  id            INT UNSIGNED  NOT NULL AUTO_INCREMENT,
    user_id       INT UNSIGNED  NOT NULL,
      languages     VARCHAR(500)  NOT NULL,
        motivation    TEXT          NOT NULL,
          experience    VARCHAR(2000) NOT NULL DEFAULT '',
            country       VARCHAR(100)  NOT NULL DEFAULT '',
              status        ENUM('pending','reviewing','accepted','rejected') NOT NULL DEFAULT 'pending',
                reviewed_by   INT UNSIGNED NULL,
                  reviewed_at   DATETIME NULL,
                    reject_reason VARCHAR(255)  NOT NULL DEFAULT '',
                      created_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
                        updated_at    DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
                          PRIMARY KEY (id),
                            UNIQUE KEY uk_ta_user (user_id),
                              KEY idx_ta_status (status),
                                CONSTRAINT fk_ta_user     FOREIGN KEY (user_id)     REFERENCES users (id) ON DELETE CASCADE,
                                  CONSTRAINT fk_ta_reviewer FOREIGN KEY (reviewed_by) REFERENCES users (id) ON DELETE SET NULL
                                  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 26. Shiny collection privacy flag (2026-06-18)
ALTER TABLE users ADD COLUMN shinies_hidden TINYINT(1) NOT NULL DEFAULT 0 AFTER directory_hidden;

-- 27. Friends and blocks (2026-06-22)
-- Friendship is directional: adding A->B means A sees notifications for B's raids; B does not see A's.
-- Blocking removes the friendship in both directions and is enforced in the social API handlers.
CREATE TABLE IF NOT EXISTS user_friends (
  user_id    INT UNSIGNED NOT NULL,
  friend_id  INT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, friend_id),
  CONSTRAINT fk_uf_user   FOREIGN KEY (user_id)   REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_uf_friend FOREIGN KEY (friend_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_blocks (
  user_id    INT UNSIGNED NOT NULL,
  blocked_id INT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, blocked_id),
  CONSTRAINT fk_ub_user    FOREIGN KEY (user_id)    REFERENCES users(id) ON DELETE CASCADE,
  CONSTRAINT fk_ub_blocked FOREIGN KEY (blocked_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 28. Feedback options -- staff-curated list of Pokemon-themed phrases (2026-06-22)
-- UNIQUE KEY on label makes the INSERT IGNORE seed safe to re-run.
CREATE TABLE IF NOT EXISTS feedback_options (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  label      VARCHAR(120) NOT NULL,
  sentiment  ENUM('positive','neutral','negative') NOT NULL DEFAULT 'neutral',
  sort_order TINYINT UNSIGNED NOT NULL DEFAULT 0,
  enabled    TINYINT(1)   NOT NULL DEFAULT 1,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_option_label (label)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO feedback_options (label, sentiment, sort_order) VALUES
  ('Great Raid Host -- always ready on time! ⭐',   'positive', 1),
  ('Helpful and welcoming in lobbies 🤝',            'positive', 2),
  ('Would raid with them again! 💪',                 'positive', 3),
  ('Friendly communicator 📢',                       'positive', 4),
  ('Kept the lobby fun and positive 🎉',             'positive', 5),
  ('No issues, standard experience 😐',              'neutral',  10),
  ('Left the lobby without notice 🚪',               'negative', 20),
  ('Went offline mid-raid 👻',                       'negative', 21),
  ('Repeated no-shows 🔴',                           'negative', 22),
  ('Rude or disrespectful in lobby 😡',              'negative', 23);

-- 29. User feedback -- one review per author/target pair; updatable via ON DUPLICATE KEY (2026-06-22)

CREATE TABLE IF NOT EXISTS user_feedback (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  author_id  INT UNSIGNED NOT NULL,
  target_id  INT UNSIGNED NOT NULL,
  option_id  INT UNSIGNED NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_feedback_pair (author_id, target_id),
  KEY idx_feedback_target (target_id),
  CONSTRAINT fk_fb_author FOREIGN KEY (author_id) REFERENCES users(id)            ON DELETE CASCADE,
  CONSTRAINT fk_fb_target FOREIGN KEY (target_id) REFERENCES users(id)            ON DELETE CASCADE,
  CONSTRAINT fk_fb_option FOREIGN KEY (option_id) REFERENCES feedback_options(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 30. Mobile companion app tables (2026-06-22)
CREATE TABLE IF NOT EXISTS user_pokemon_box (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        INT UNSIGNED NOT NULL,
  pokemon_name   VARCHAR(64) NOT NULL,
  form           VARCHAR(64) NOT NULL DEFAULT '',
  cp             SMALLINT UNSIGNED NOT NULL,
  level          DECIMAL(4,1) NOT NULL,
  atk_iv         TINYINT UNSIGNED,
  def_iv         TINYINT UNSIGNED,
  sta_iv         TINYINT UNSIGNED,
  iv_candidates  JSON,
  caught_at      DATETIME,
  note           VARCHAR(160),
  created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_upb_user (user_id),
  CONSTRAINT fk_upb_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS mobile_device_tokens (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id        INT UNSIGNED NOT NULL,
  platform       ENUM('android','ios') NOT NULL,
  push_token     VARCHAR(256) NOT NULL,
  device_name    VARCHAR(128),
  created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY idx_mdt_token (push_token),
  KEY idx_mdt_user (user_id),
  CONSTRAINT fk_mdt_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 31. Event Pokémon support in shiny collection (2026-06-23)
ALTER TABLE user_shinies
  ADD COLUMN costume   VARCHAR(64)  NOT NULL DEFAULT '' AFTER form,
  ADD COLUMN event_tag VARCHAR(128) NOT NULL DEFAULT '' AFTER costume,
  DROP INDEX uk_user_shiny,
  ADD UNIQUE KEY uk_user_shiny (user_id, pokemon_id, form, costume, event_tag);

-- 32. Trainer level field (2026-06-23)
ALTER TABLE users ADD COLUMN trainer_level TINYINT UNSIGNED NOT NULL DEFAULT 0 AFTER trainer_code;

-- 33. Drop unique constraint on user_shinies to allow true duplicate shiny entries (2026-06-23)
-- Add replacement index first so MySQL has an alternative for the FK before dropping the unique one.
ALTER TABLE user_shinies ADD INDEX idx_user_shiny (user_id, pokemon_id);
ALTER TABLE user_shinies DROP INDEX uk_user_shiny;

-- 34. Rename user_friends to user_follows (2026-06-23)
-- Converts the "Add Friend" concept to a followers/following model.
-- Semantics unchanged: user_id follows friend_id. All FKs, indexes, and data preserved.
RENAME TABLE user_friends TO user_follows;

-- 35. Confirm timeout warning flag on raid_lobby_members (2026-06-25)
ALTER TABLE raid_lobby_members ADD COLUMN confirm_warned_30s TINYINT(1) NOT NULL DEFAULT 0;

-- 36. Per-award minimum grant rank (2026-06-25)
-- 0=Anyone  1=Trusted  2=ContentCreator  3=Translator  4=Tester  5=Moderator
-- Admins always bypass via IsMod() check in the handler.
ALTER TABLE awards ADD COLUMN min_grant_rank TINYINT UNSIGNED NOT NULL DEFAULT 0;

-- 37. Trainer avatar sprite locks (2026-06-26)
-- 0=all  1=Trusted+  2=ContentCreator+  4=Tester+  5=Moderator+  100=Admin only
-- Professor sprites (label starts "Prof.") are always enforced at rank 1 in code.
-- This table covers admin-configurable locks for all other sprites.
CREATE TABLE IF NOT EXISTS sprite_locks (
  slug       VARCHAR(100) NOT NULL,
  min_rank   TINYINT UNSIGNED NOT NULL DEFAULT 0,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 38. Shiny evolved_at (2026-06-26)
ALTER TABLE user_shinies ADD COLUMN evolved_at DATETIME NULL DEFAULT NULL;

-- 39. Bug report system "Report Me Not" (2026-06-28)
-- Lightweight ticketing: threaded messaging between reporters and staff, labels,
-- multi-user invites, and private staff-only notes. reporter_id NULL / anon_token
-- are reserved for the deferred anonymous (logged-out) flow.
CREATE TABLE IF NOT EXISTS bug_reports (
  id               INT UNSIGNED NOT NULL AUTO_INCREMENT,
  reporter_id      INT UNSIGNED NULL,
  reporter_email   VARCHAR(255) NULL,
  subject          VARCHAR(160) NOT NULL,
  status           ENUM('open','pending','resolved','closed') NOT NULL DEFAULT 'open',
  anon_token       CHAR(64)     NULL,
  created_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  last_activity_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_br_status (status),
  KEY idx_br_reporter (reporter_id),
  UNIQUE KEY uk_br_anon_token (anon_token),
  CONSTRAINT fk_br_reporter FOREIGN KEY (reporter_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS bug_report_messages (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  report_id  INT UNSIGNED NOT NULL,
  author_id  INT UNSIGNED NULL,
  body       TEXT NOT NULL,
  visibility ENUM('public','internal') NOT NULL DEFAULT 'public',
  is_system  TINYINT(1) NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_brm_report (report_id, created_at),
  CONSTRAINT fk_brm_report FOREIGN KEY (report_id) REFERENCES bug_reports (id) ON DELETE CASCADE,
  CONSTRAINT fk_brm_author FOREIGN KEY (author_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS bug_report_participants (
  report_id    INT UNSIGNED NOT NULL,
  user_id      INT UNSIGNED NOT NULL,
  role         ENUM('reporter','collaborator','staff') NOT NULL DEFAULT 'collaborator',
  added_by     INT UNSIGNED NULL,
  last_seen_at DATETIME   NULL,
  created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (report_id, user_id),
  KEY idx_brp_user (user_id),
  CONSTRAINT fk_brp_report FOREIGN KEY (report_id) REFERENCES bug_reports (id) ON DELETE CASCADE,
  CONSTRAINT fk_brp_user   FOREIGN KEY (user_id)   REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS bug_report_labels (
  id      INT UNSIGNED NOT NULL AUTO_INCREMENT,
  name    VARCHAR(40)  NOT NULL,
  color   VARCHAR(7)   NOT NULL DEFAULT '#cccccc',
  builtin TINYINT(1)   NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  UNIQUE KEY uk_brl_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO bug_report_labels (name, color, builtin) VALUES
  ('Bug',             '#e53935', 1),
  ('Crash',           '#b71c1c', 1),
  ('UI',              '#5b9cf6', 1),
  ('Feature Request', '#00d68f', 1),
  ('Question',        '#a78bfa', 1),
  ('Duplicate',       '#8888b8', 1),
  ('Wontfix',         '#55558a', 1);

CREATE TABLE IF NOT EXISTS bug_report_label_map (
  report_id INT UNSIGNED NOT NULL,
  label_id  INT UNSIGNED NOT NULL,
  PRIMARY KEY (report_id, label_id),
  CONSTRAINT fk_brlm_report FOREIGN KEY (report_id) REFERENCES bug_reports (id) ON DELETE CASCADE,
  CONSTRAINT fk_brlm_label  FOREIGN KEY (label_id)  REFERENCES bug_report_labels (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 40. Bug reports triage enhancements (2026-06-28)
-- Adds priority, assignee, and CSAT rating to reports, plus a canned-responses
-- (macros) table. The ALTER lines are one-time -- skip any that already applied.
ALTER TABLE bug_reports
  ADD COLUMN priority       ENUM('low','normal','high','urgent') NOT NULL DEFAULT 'normal' AFTER status,
  ADD COLUMN assignee_id    INT UNSIGNED NULL AFTER priority,
  ADD COLUMN rating         ENUM('','good','bad') NOT NULL DEFAULT '' AFTER assignee_id,
  ADD COLUMN rating_comment VARCHAR(500) NOT NULL DEFAULT '' AFTER rating,
  ADD COLUMN rated_at       DATETIME NULL AFTER rating_comment,
  ADD KEY idx_br_assignee (assignee_id),
  ADD KEY idx_br_priority (priority),
  ADD CONSTRAINT fk_br_assignee FOREIGN KEY (assignee_id) REFERENCES users (id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS bug_report_macros (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  title      VARCHAR(80)  NOT NULL,
  body       TEXT         NOT NULL,
  created_by INT UNSIGNED NULL,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT fk_brmac_creator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 41. Player ("bad actor") report system (2026-06-28)
-- Adds a type discriminator, reported-user reference, and reason so player
-- reports share the ticket tables. ALTER lines are one-time -- skip if applied.
ALTER TABLE bug_reports
  ADD COLUMN type             ENUM('bug','player') NOT NULL DEFAULT 'bug' AFTER id,
  ADD COLUMN reported_user_id INT UNSIGNED NULL AFTER reporter_id,
  ADD COLUMN reason           VARCHAR(64) NOT NULL DEFAULT '' AFTER subject,
  ADD KEY idx_br_type (type, status),
  ADD KEY idx_br_reported (reported_user_id),
  ADD CONSTRAINT fk_br_reported FOREIGN KEY (reported_user_id) REFERENCES users (id) ON DELETE SET NULL;


-- 42. Transactional email: email_verified_at + email_tokens (2026-07-03)
-- Soft email verification and password reset tokens. Existing accounts are
-- grandfathered as verified. Raw tokens are never stored, only SHA-256 hashes;
-- rows are single-use (used_at) and short-lived (expires_at).
ALTER TABLE users
  ADD COLUMN email_verified_at DATETIME NULL DEFAULT NULL AFTER email;

UPDATE users SET email_verified_at = NOW() WHERE email_verified_at IS NULL;

CREATE TABLE IF NOT EXISTS email_tokens (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    INT UNSIGNED NOT NULL,
  token_hash CHAR(64)     NOT NULL,
  purpose    ENUM('verify','reset') NOT NULL,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME     NOT NULL,
  used_at    DATETIME     NULL DEFAULT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_hash (token_hash),
  KEY idx_et_user (user_id, purpose),
  KEY idx_et_expires (expires_at),
  CONSTRAINT fk_et_user FOREIGN KEY (user_id)
    REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;


-- 43. Regional form support in shiny collection (2026-07-05)
-- Adds a region dimension (alolan, galarian, hisuian, paldean, or '' for the
-- original form) to user shiny entries. Kept separate from form so combos
-- like Shadow Alolan stay representable.
ALTER TABLE user_shinies
  ADD COLUMN region VARCHAR(16) NOT NULL DEFAULT '' AFTER form;


-- 44. Shiny dex availability overrides (2026-07-25)
-- The full National Dex now ships as an embedded baseline (in_go and
-- shiny_released per species, and per regional form). This table holds ONLY
-- admin corrections to that baseline, so it stays sparse and shipping a
-- corrected baseline later does not fight the admin.
--
-- region '' is the species row; any other value is a regional or alternate
-- form row, keyed the same way user_shinies.region is. A NULL column means
-- "no opinion, use the baseline default"; a row that matches the default in
-- every column is DELETEd rather than stored.
CREATE TABLE IF NOT EXISTS shiny_dex_overrides (
  dex            SMALLINT UNSIGNED NOT NULL,
  region         VARCHAR(16)       NOT NULL DEFAULT '',
  in_go          TINYINT(1)        NULL DEFAULT NULL,
  shiny_released TINYINT(1)        NULL DEFAULT NULL,
  note           VARCHAR(255)      NOT NULL DEFAULT '',
  updated_by     INT UNSIGNED      NULL DEFAULT NULL,
  updated_at     DATETIME          NOT NULL DEFAULT CURRENT_TIMESTAMP
                                   ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (dex, region),
  CONSTRAINT fk_sdo_user FOREIGN KEY (updated_by)
    REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;


-- 45. Announced shiny release dates (2026-07-27)
-- Niantic announces a shiny weeks before it ships. This column records the
-- announced day so the admin panel can show it, the checklist can show it, and
-- the shiny turns itself on when the day arrives instead of waiting for
-- somebody to remember to tick a box.
--
-- DATE, not DATETIME: an announced release is a calendar day, not an instant,
-- and the rest of this feature already compares dates as 'YYYY-MM-DD' strings
-- so there is no midnight-in-whose-timezone question to get wrong.
--
-- An explicit shiny_released still beats a passed date in both directions, so a
-- delayed release is one unticked box away from being corrected.
ALTER TABLE shiny_dex_overrides
  ADD COLUMN release_date DATE NULL DEFAULT NULL AFTER shiny_released;


-- 46. Nullable strike issuer (2026-08-05)
-- Staff accounts can now be deleted outright from the admin panel, and
-- user_strikes.issued_by was NOT NULL with ON DELETE CASCADE. That meant deleting
-- a moderator also deleted every strike that moderator had ever issued, wiping the
-- moderation record of the trainers they disciplined.
--
-- A strike is a record about the trainer it was issued against, not about the
-- issuer, so it has to outlive the issuer. issued_by_name already holds a snapshot
-- of the issuer's username and is the only issuer field the panel reads
-- (AdminStrikesGet), so SET NULL costs nothing the UI shows.
ALTER TABLE user_strikes DROP FOREIGN KEY fk_strike_issuer;
ALTER TABLE user_strikes MODIFY issued_by INT UNSIGNED NULL DEFAULT NULL;
ALTER TABLE user_strikes
  ADD CONSTRAINT fk_strike_issuer FOREIGN KEY (issued_by)
  REFERENCES users (id) ON DELETE SET NULL;


-- 47. Shadow and purified status on a boxed Pokemon (2026-08-05)
-- The raids page now scores a trainer's own Pokemon against the boss they have
-- open, and shadow is worth +20% attack (and 20% more damage taken) in that
-- calculation, which is often the difference between a good counter and the best
-- one. The box recorded species, form, CP, level and IVs but not this, so a
-- Shadow Machamp scored as an ordinary Machamp.
--
-- The IV calculator already asks (it needs the flags to read the dust cost); it
-- simply had nowhere to put the answer.
--
-- Nullable rather than NOT NULL DEFAULT 0: existing rows genuinely do not know,
-- and "not recorded" is a different statement from "not a shadow". The UI can
-- then offer to fill it in rather than silently asserting a default.
ALTER TABLE user_pokemon_box
  ADD COLUMN is_shadow   TINYINT(1) NULL DEFAULT NULL AFTER form,
  ADD COLUMN is_purified TINYINT(1) NULL DEFAULT NULL AFTER is_shadow;

-- 48. Room for every language a translator applicant can list (2026-08-27)
-- translator_applications.languages holds the applicant's answer as JSON, one
-- object per language: [{"code":"de","level":"intermediate"}, ...]. The form
-- offers 13 languages plus a free text "other" box capped at 50 characters, and
-- with all 14 selected at the longest level that JSON is 573 characters. The
-- column was VARCHAR(500), so the insert failed and the applicant was turned
-- away with a generic error and no way through.
--
-- 1000 leaves headroom for roughly eleven more languages before this needs
-- revisiting. TestTranslatorLanguagesFitColumn asserts the worst case still fits,
-- so adding a language to supportedApplyLangs fails the build rather than the
-- applicant.
ALTER TABLE translator_applications MODIFY languages VARCHAR(1000) NOT NULL;


-- 49. Event reminder subscriptions (2026-08-27)
-- The app has had a bell on every upcoming event card for a release now, with a
-- per-event lead time and a default in Settings, and nothing behind it: the calls
-- 404'd and the app fell back to its disk cache. This is the store behind them.
--
-- There is no foreign key on event_id because there is no events table. The feed
-- is an in-memory blob mirrored to cache/events.json, so event_id is a loose
-- string; the reconcile that runs after every feed refresh cancels rows whose id
-- has left the feed, and that is what keeps it honest.
--
-- event_start is stored alongside the id, and is not the same as starts_at.
-- starts_at is the absolute instant resolved in the subscriber's timezone;
-- event_start is the feed's own start reading, always parsed as UTC, so it is a
-- zone-independent fingerprint of what upstream said when the trainer subscribed.
-- Comparing it against the feed on refresh is what tells a genuine time change
-- apart from a different occurrence reusing a slug. Recurring Spotlight Hours
-- embed their date in the id (pokemonspotlighthour2026-08-27), but GO Battle
-- League slugs do not: gbl-forever-forward_great-league_ultra-league names the
-- league split and can legitimately come round again on a later rotation.
--
-- reminded_at and started_at_sent are flag columns in the shape processRaidTimers
-- already uses: set before the push is dispatched, so a re-send needs a crash at
-- exactly the wrong moment rather than a slow HTTP call.
CREATE TABLE IF NOT EXISTS event_subscriptions (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id         INT UNSIGNED NOT NULL,
  event_id        VARCHAR(128) NOT NULL,
  lead_minutes    SMALLINT UNSIGNED NOT NULL DEFAULT 30,
  timezone        VARCHAR(64) NOT NULL,
  event_start     DATETIME NOT NULL,
  remind_at       DATETIME NULL,
  starts_at       DATETIME NOT NULL,
  reminded_at     DATETIME NULL,
  started_at_sent DATETIME NULL,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY idx_evsub_user_event (user_id, event_id),
  KEY idx_evsub_due (remind_at, reminded_at),
  KEY idx_evsub_start (starts_at, started_at_sent),
  CONSTRAINT fk_evsub_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- 50. Several reminders per event subscription (2026-08-27)
-- A trainer can now ask for a day's warning AND a thirty minute one for the same
-- event: one to plan around, one to stop what you are doing. lead_minutes was a
-- scalar, so it becomes a set, and the three columns that describe ONE
-- notification move off the parent onto a row each.
--
-- The split is not cosmetic. timezone, event_start, starts_at and
-- started_at_sent are genuinely properties of the subscription: the zone it was
-- pinned in, the start it was pinned to, and whether the at-start push has gone
-- out. lead_minutes, remind_at and reminded_at are properties of one reminder.
--
-- reminded_at moving to the child row is the load bearing part. Left on the
-- parent, the first push of the evening marks the subscription sent and silently
-- eats the rest, and the symptom ("I only ever get the earliest one") is very
-- hard to notice and harder still to attribute.
--
-- UNIQUE (subscription_id, lead_minutes) is what makes the upsert idempotent and
-- stops a repeated lead time turning into two identical pushes.
CREATE TABLE IF NOT EXISTS event_subscription_reminders (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  subscription_id BIGINT UNSIGNED NOT NULL,
  lead_minutes    SMALLINT UNSIGNED NOT NULL,
  remind_at       DATETIME NULL,
  reminded_at     DATETIME NULL,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY idx_esr_sub_lead (subscription_id, lead_minutes),
  KEY idx_esr_due (remind_at, reminded_at),
  CONSTRAINT fk_esr_sub FOREIGN KEY (subscription_id)
    REFERENCES event_subscriptions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Carry the existing subscriptions across rather than starting empty. There are
-- live rows and they are somebody's reminders.
INSERT IGNORE INTO event_subscription_reminders
    (subscription_id, lead_minutes, remind_at, reminded_at)
SELECT id, lead_minutes, remind_at, reminded_at FROM event_subscriptions;

-- event_subscriptions.lead_minutes, remind_at and reminded_at are deliberately
-- NOT dropped. Build 15 of the app is on testers' phones and reads the first two
-- off the response, and dropping a column is the one migration that cannot be
-- fixed by re-running this file. The server keeps them written as a mirror of
-- the first reminder and reads neither: event_subscription_reminders is the only
-- source of truth from here. They come out once no install below 16 is in use.

-- 51. Where a boxed Pokemon came from (2026-08-30)
-- The box is about to become something other people can see: a profile will show
-- dex completion from it, beside the shiny dex. Until now every row was private,
-- so a wrong entry only ever misled the person who typed it, and nothing needed
-- to record how a row arrived. Once a collection has an audience, a fake one is
-- a reputational gain, and a display has to be able to tell a scanned entry from
-- a typed one.
--
-- This is why the column goes in before the profile feature rather than with it.
-- Origin is not recoverable after the fact: a row already in the table carries
-- no trace of how it was created, and no migration can invent one. Every row
-- added between now and the column landing would be permanently unattributable,
-- so the cost of waiting grows with the delay.
--
-- Existing rows become 'unknown', which is deliberately not 'manual'. "We do not
-- know" and "a human typed it" are different claims, and only the first one is
-- true of them.
--
-- The value is derived server side from the route the write arrived on and what
-- the client claimed, never copied from the request: 'scan_app_attested' means
-- an integrity check actually passed, so a client must not be able to write it
-- by asking. See writeProvenance in internal/handlers/iv.go.
ALTER TABLE user_pokemon_box
  ADD COLUMN provenance ENUM('manual','scan_web','scan_app','scan_app_attested','unknown')
      NOT NULL DEFAULT 'unknown' AFTER note;

-- 52. Raid boss history, as a star schema (2026-08-31)
-- cache/raids.json is overwritten on every refresh, so the day a rotation ends it
-- is gone. Nothing could answer "what was in tier 5 last week" or "when did this
-- boss last run", and the events feed prunes a rotation a day or so after it ends,
-- which is already recorded as a source of trouble in raidschedule.go.
--
-- Two tables rather than one, because a flat table would repeat a boss's stats,
-- typing, CP range and sprite on every appearance: the same few hundred bytes
-- rewritten every couple of hours forever, and no single row to compare against
-- when asking whether a boss has changed. So the expensive part is resolved ONCE,
-- on first sight, and every later sighting is a narrow row pointing at it.
--
-- raid_boss_dim is the dimension. The natural key is (species, form, tier, shadow):
-- the same species at tier 5 and as a Mega are different bosses with different
-- stats, and a shadow is a different boss again.
--
-- appearance_count and last_seen_at are denormalised on purpose. They are the two
-- questions asked most often about a boss, and answering either from the fact
-- table alone is a scan.
--
-- Slowly changing dimension, TYPE 1: a rebalance overwrites in place and moves
-- stats_updated_at. Type 2 versioning would let the site answer "what were this
-- boss's stats last March", which nothing asks and which doubles the join on every
-- read. If that is ever wanted it is additive: add valid_from/valid_to and stop
-- overwriting.
CREATE TABLE IF NOT EXISTS raid_boss_dim (
  id                INT UNSIGNED NOT NULL AUTO_INCREMENT,
  species           VARCHAR(96)  NOT NULL,
  form              VARCHAR(64)  NOT NULL DEFAULT '',
  tier              VARCHAR(8)   NOT NULL,
  shadow            TINYINT(1)   NOT NULL DEFAULT 0,
  is_mega           TINYINT(1)   NOT NULL DEFAULT 0,
  types             VARCHAR(64)  NOT NULL DEFAULT '',
  cp_min            INT UNSIGNED NOT NULL DEFAULT 0,
  cp_max            INT UNSIGNED NOT NULL DEFAULT 0,
  cp_boosted_min    INT UNSIGNED NOT NULL DEFAULT 0,
  cp_boosted_max    INT UNSIGNED NOT NULL DEFAULT 0,
  image_url         VARCHAR(512) NOT NULL DEFAULT '',
  can_be_shiny      TINYINT(1)   NOT NULL DEFAULT 0,
  first_seen_at     DATETIME     NOT NULL,
  last_seen_at      DATETIME     NOT NULL,
  appearance_count  INT UNSIGNED NOT NULL DEFAULT 0,
  stats_updated_at  DATETIME     NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_raid_boss (species, form, tier, shadow),
  KEY idx_raid_boss_last_seen (last_seen_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- raid_appearance_fact is the fact table: one narrow row per boss per rotation.
--
-- No stats, no sprite, no typing. Anything derivable by joining the dimension does
-- not belong here.
--
-- The unique key is doing real work rather than being defensive decoration: the
-- served list is rebuilt on a short ticker and on every refresh, so the same
-- rotation is offered for recording many times over its run.
--
-- Retention: KEPT INDEFINITELY, on purpose. The dimension is small and permanent;
-- this grows at roughly one row per boss per rotation, which is a few thousand rows
-- a year. Do not add a cleanup job without deciding that history is not wanted.
CREATE TABLE IF NOT EXISTS raid_appearance_fact (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  boss_id       INT UNSIGNED    NOT NULL,
  window_start  DATETIME        NOT NULL,
  window_end    DATETIME        NOT NULL,
  event_id      VARCHAR(128)    NOT NULL DEFAULT '',
  -- 'upstream' came from the raid feed; 'events' is a card this app built from the
  -- rotation schedule because the feed had not listed the boss yet. Worth keeping:
  -- a later reader should be able to tell a published boss from an inferred one.
  source        ENUM('upstream','events') NOT NULL DEFAULT 'upstream',
  recorded_at   DATETIME        NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uniq_raid_appearance (boss_id, window_start),
  KEY idx_raid_appearance_window (window_start),
  CONSTRAINT fk_raid_appearance_boss FOREIGN KEY (boss_id)
    REFERENCES raid_boss_dim (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 53. Marking an account for deletion instead of removing it (2026-08-31)
-- Account deletion becomes a MARK plus a delayed purge rather than an immediate row
-- delete. deleted_at NULL means a live account; a timestamp means the account is gone
-- as far as the site is concerned and its row is waiting to be purged.
--
-- The index is on deleted_at alone because the purge sweep is the only query that
-- ranges on it. Every other read tests it for NULL alongside disabled, which is
-- already covered by the lookups those queries use.
ALTER TABLE users ADD COLUMN deleted_at DATETIME NULL DEFAULT NULL AFTER disabled_reason;
CREATE INDEX idx_users_deleted_at ON users (deleted_at);
