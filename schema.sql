-- hailsdotgo schema -- fresh install
-- Run this once on a new MySQL server to create the database and all tables.
-- Existing installs upgrading from an older version: run migrate.sql instead.

CREATE DATABASE IF NOT EXISTS hailsdotgo
  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
USE hailsdotgo;

CREATE TABLE IF NOT EXISTS users (
  id               INT UNSIGNED  NOT NULL AUTO_INCREMENT,
  username         VARCHAR(32)   NOT NULL,
  email            VARCHAR(255)  NOT NULL,
  lang             VARCHAR(8)    NOT NULL DEFAULT 'en',
  password         VARCHAR(60)   NOT NULL,
  role             ENUM('user','tester','moderator','admin') NOT NULL DEFAULT 'user',
  pending_role     ENUM('tester','moderator','admin') NULL DEFAULT NULL,
  api_access       TINYINT(1)    NOT NULL DEFAULT 0,
  translator       TINYINT(1)    NOT NULL DEFAULT 0,
  disabled         TINYINT(1)    NOT NULL DEFAULT 0,
  disabled_reason  VARCHAR(255)  NOT NULL DEFAULT '',
  created_at       DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  trainer_name     VARCHAR(64)   NOT NULL DEFAULT '',
  trainer_code     VARCHAR(16)   NOT NULL DEFAULT '',
  trainer_level    TINYINT UNSIGNED NOT NULL DEFAULT 0,
  avatar           VARCHAR(32)   NOT NULL DEFAULT '',
  pronouns         VARCHAR(32)   NOT NULL DEFAULT '',
  city             VARCHAR(100)  NOT NULL DEFAULT '',
  region           VARCHAR(100)  NOT NULL DEFAULT '',
  country          VARCHAR(100)  NOT NULL DEFAULT '',
  location_display ENUM('none','country','full') NOT NULL DEFAULT 'none',
  profile_public   TINYINT(1)    NOT NULL DEFAULT 0,
  directory_hidden TINYINT(1)    NOT NULL DEFAULT 0,
  shinies_hidden   TINYINT(1)    NOT NULL DEFAULT 0,
  raid_banned      TINYINT(1)    NOT NULL DEFAULT 0,
  fav_pokemon      VARCHAR(64)   NOT NULL DEFAULT '',
  fav_pokemon_form VARCHAR(32)   NOT NULL DEFAULT '',
  fav_sprite_url   VARCHAR(255)  NOT NULL DEFAULT '',
  raid_xp          INT UNSIGNED  NOT NULL DEFAULT 0,
  last_raid_at     DATETIME      NULL,
  rater_weight     DECIMAL(4,3)  NOT NULL DEFAULT 1.000,
  trust_score      DECIMAL(7,2)  NOT NULL DEFAULT 0.00,
  special_rank     ENUM('','trusted','content_creator') NOT NULL DEFAULT '',
  last_seen_at     DATETIME      NULL,
  tag_requested_at DATETIME      NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_username (username),
  UNIQUE KEY uk_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS sessions (
  token      CHAR(64)     NOT NULL,
  user_id    INT UNSIGNED NOT NULL,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at DATETIME     NOT NULL,
  PRIMARY KEY (token),
  KEY idx_expires (expires_at),
  CONSTRAINT fk_session_user FOREIGN KEY (user_id)
    REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- form is '' for the default variant; shadow/purified/etc. are separate rows.
-- pokemon_id matches the name key from PoGoAPI shinies (e.g. "Bulbasaur").
CREATE TABLE IF NOT EXISTS user_shinies (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    INT UNSIGNED NOT NULL,
  pokemon_id VARCHAR(64)  NOT NULL,
  form       VARCHAR(32)  NOT NULL DEFAULT '',
  costume    VARCHAR(64)  NOT NULL DEFAULT '',
  event_tag  VARCHAR(128) NOT NULL DEFAULT '',
  method     VARCHAR(32)  NOT NULL DEFAULT '',
  caught_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  evolved_at DATETIME     NULL DEFAULT NULL,
  PRIMARY KEY (id),
  KEY idx_user_shiny (user_id, pokemon_id),
  CONSTRAINT fk_shiny_user FOREIGN KEY (user_id)
    REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Migration 33 (2026-06-23): drop unique constraint to allow true duplicate shiny entries
-- ALTER TABLE user_shinies ADD INDEX idx_user_shiny (user_id, pokemon_id);
-- ALTER TABLE user_shinies DROP INDEX uk_user_shiny;

CREATE TABLE IF NOT EXISTS site_settings (
  setting_key   VARCHAR(64)  NOT NULL,
  setting_value VARCHAR(255) NOT NULL DEFAULT '',
  PRIMARY KEY (setting_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO site_settings (setting_key, setting_value) VALUES
  ('registration_open',                 '0'),
  ('page_raids_enabled',                '1'),
  ('page_dps_enabled',                  '1'),
  ('page_pvp_enabled',                  '1'),
  ('page_events_enabled',               '1'),
  ('page_trainers_enabled',             '1'),
  ('section_trainer_directory_enabled', '1'),
  ('section_raid_finder_enabled',       '1'),
  ('page_shinies_enabled',              '1'),
  ('store_enabled',                     '0'),
  ('awards_community_grants_enabled',   '0'),
  ('awards_grant_min_trust',            '50'),
  ('raid_custom_lobby_min_trust',       '50');

CREATE TABLE IF NOT EXISTS invites (
  token        VARCHAR(64)  NOT NULL,
  created_by   INT UNSIGNED NOT NULL,
  granted_role ENUM('user','tester','moderator','admin') NOT NULL DEFAULT 'user',
  created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at   DATETIME     NOT NULL,
  max_uses     TINYINT UNSIGNED NOT NULL DEFAULT 1,
  use_count    TINYINT UNSIGNED NOT NULL DEFAULT 0,
  used_by      INT UNSIGNED DEFAULT NULL,
  used_at      DATETIME     DEFAULT NULL,
  PRIMARY KEY (token),
  KEY idx_invite_expires (expires_at),
  CONSTRAINT fk_invite_creator FOREIGN KEY (created_by)
    REFERENCES users (id) ON DELETE CASCADE,
  CONSTRAINT fk_invite_used FOREIGN KEY (used_by)
    REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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

INSERT IGNORE INTO store_items (name, slug, description, price_cents, type, benefit, sort_order) VALUES
  ('Supporter Pack', 'supporter',
   'Submit a custom tag (staff-reviewed) that appears on your trainer profile. Includes a Supporter badge.',
   299, 'bimonthly', 'supporter', 1),
  ('Priority Pass', 'priority_pass',
   'Jump to the front of the raid join queue for 30 days.',
   799, 'monthly', 'queue_priority', 2);

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
  status        ENUM('pending','approved','rejected','revision') NOT NULL DEFAULT 'pending',
  reviewed_by   INT UNSIGNED NULL,
  reviewed_at   DATETIME NULL,
  reject_reason VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_ctr_status (status),
  CONSTRAINT fk_ctr_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tag_color_changes (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    INT UNSIGNED NOT NULL,
  changed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_tcc_user_time (user_id, changed_at),
  CONSTRAINT fk_tcc_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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

CREATE TABLE IF NOT EXISTS locales (
  code       CHAR(2)      NOT NULL,
  enabled    TINYINT(1)   NOT NULL DEFAULT 0,
  created_by INT UNSIGNED NULL,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (code),
  CONSTRAINT fk_loc_creator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO locales (code, enabled) VALUES ('es', 1), ('fr', 1), ('de', 1);

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

-- Legacy raid finder tables (v1). Still referenced by some code paths; v2 writes
-- to raid_lobbies instead. These can be dropped once v2 has been stable.
CREATE TABLE IF NOT EXISTS raid_posts (
  id              INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id         INT UNSIGNED NOT NULL,
  boss_name       VARCHAR(64)  NOT NULL,
  boss_tier       TINYINT UNSIGNED NOT NULL DEFAULT 0,
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
  status       VARCHAR(16)  NOT NULL DEFAULT 'accepted',
  priority     TINYINT(1)   NOT NULL DEFAULT 0,
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
  host_vote          ENUM('none','commend','dislike') NOT NULL DEFAULT 'none',
  confirm_warned_30s TINYINT(1)   NOT NULL DEFAULT 0,
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
CREATE TABLE IF NOT EXISTS trust_events (
  id            INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id       INT UNSIGNED NOT NULL,
  actor_id      INT UNSIGNED NULL,
  lobby_id      INT UNSIGNED NULL,
  event_type    ENUM('commend','dislike','confirm_timeout','invite_window_fail','left_early','raid_success','staff_adjust','host_unfulfilled') NOT NULL,
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

CREATE TABLE IF NOT EXISTS awards (
  id          INT UNSIGNED NOT NULL AUTO_INCREMENT,
  slug        VARCHAR(32)  NOT NULL,
  name        VARCHAR(64)  NOT NULL,
  description VARCHAR(255) NOT NULL DEFAULT '',
  icon        VARCHAR(16)  NOT NULL DEFAULT '🏆',
  color       VARCHAR(7)   NOT NULL DEFAULT '#f0b429',
  active      TINYINT(1)   NOT NULL DEFAULT 1,
  sort_order     TINYINT UNSIGNED NOT NULL DEFAULT 0,
  min_grant_rank TINYINT UNSIGNED NOT NULL DEFAULT 0,
  created_by  INT UNSIGNED NULL,
  created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_award_slug (slug),
  CONSTRAINT fk_award_creator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO awards (slug, name, description, icon, color, sort_order) VALUES
  ('helping-hand',    'Helping Hand',      'Goes out of their way to help other trainers',              '🤝', '#4caf50', 1),
  ('friend-ball',     'Friend Ball',       'Welcoming and friendly to everyone in lobbies',             '💚', '#7bd389', 2),
  ('professors-aide', 'Professor''s Aide', 'Great teacher: explains counters, mechanics, and strategy', '📚', '#5b9cf6', 3),
  ('ace-trainer',     'Ace Trainer',       'A role model for the community',                            '⭐', '#f0b429', 4),
  ('joyful-spirit',   'Joyful Spirit',     'Keeps raids fun and positive',                              '🎉', '#e91e63', 5);

-- Same award from different granters stacks; one granter cannot repeat it.
CREATE TABLE IF NOT EXISTS award_grants (
  id           INT UNSIGNED NOT NULL AUTO_INCREMENT,
  award_id     INT UNSIGNED NOT NULL,
  recipient_id INT UNSIGNED NOT NULL,
  granter_id   INT UNSIGNED NULL,
  note         VARCHAR(160) NOT NULL DEFAULT '',
  created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_grant_once (award_id, recipient_id, granter_id),
  KEY idx_grant_recipient (recipient_id),
  CONSTRAINT fk_ag_award     FOREIGN KEY (award_id)     REFERENCES awards (id) ON DELETE CASCADE,
  CONSTRAINT fk_ag_recipient FOREIGN KEY (recipient_id) REFERENCES users (id)  ON DELETE CASCADE,
  CONSTRAINT fk_ag_granter   FOREIGN KEY (granter_id)   REFERENCES users (id)  ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Mobile companion app: saved Pokemon IV appraisals
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

-- Mobile companion app: FCM (Android) and APNs (iOS) push notification device tokens
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

-- Trainer avatar access locks: min_rank required to select an avatar slug.
-- 0=all, 1=trusted+, 2=content_creator+, 4=tester+, 5=moderator+, 100=admin+
-- Professors are auto-locked at rank 1 in code; this table covers admin-configured locks.
CREATE TABLE IF NOT EXISTS sprite_locks (
  slug       VARCHAR(100) NOT NULL,
  min_rank   TINYINT UNSIGNED NOT NULL DEFAULT 0,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (slug)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- After first deploy: register your admin account via the UI, then run:
--   UPDATE users SET role = 'admin' WHERE username = 'yourusername';
