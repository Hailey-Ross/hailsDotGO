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
  email_verified_at DATETIME     NULL DEFAULT NULL,
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

-- Email verification / password reset tokens. Raw tokens are emailed to the
-- user; only the SHA-256 hash is stored. Single-use via used_at.
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

-- form is '' for the default variant; shadow/purified/etc. are separate rows.
-- region is '' for the original form, else alolan/galarian/hisuian/paldean;
-- kept separate from form so combos like Shadow Alolan stay representable.
-- pokemon_id matches the name key from PoGoAPI shinies (e.g. "Bulbasaur").
CREATE TABLE IF NOT EXISTS user_shinies (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id    INT UNSIGNED NOT NULL,
  pokemon_id VARCHAR(64)  NOT NULL,
  form       VARCHAR(32)  NOT NULL DEFAULT '',
  region     VARCHAR(16)  NOT NULL DEFAULT '',
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

-- Bug report system ("Report Me Not"): lightweight ticketing with threaded
-- messaging between reporters and staff, labels, invites, and private notes.
-- reporter_id NULL and anon_token are reserved for the deferred anonymous flow;
-- this round always sets reporter_id and leaves the anon columns empty.
CREATE TABLE IF NOT EXISTS bug_reports (
  id               INT UNSIGNED NOT NULL AUTO_INCREMENT,
  type             ENUM('bug','player') NOT NULL DEFAULT 'bug',
  reporter_id      INT UNSIGNED NULL,
  reported_user_id INT UNSIGNED NULL,
  reporter_email   VARCHAR(255) NULL,
  subject          VARCHAR(160) NOT NULL,
  reason           VARCHAR(64)  NOT NULL DEFAULT '',
  status           ENUM('open','pending','resolved','closed') NOT NULL DEFAULT 'open',
  priority         ENUM('low','normal','high','urgent') NOT NULL DEFAULT 'normal',
  assignee_id      INT UNSIGNED NULL,
  rating           ENUM('','good','bad') NOT NULL DEFAULT '',
  rating_comment   VARCHAR(500) NOT NULL DEFAULT '',
  rated_at         DATETIME     NULL,
  anon_token       CHAR(64)     NULL,
  created_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  last_activity_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_br_status (status),
  KEY idx_br_reporter (reporter_id),
  KEY idx_br_assignee (assignee_id),
  KEY idx_br_priority (priority),
  KEY idx_br_type (type, status),
  KEY idx_br_reported (reported_user_id),
  UNIQUE KEY uk_br_anon_token (anon_token),
  CONSTRAINT fk_br_reporter FOREIGN KEY (reporter_id) REFERENCES users (id) ON DELETE SET NULL,
  CONSTRAINT fk_br_assignee FOREIGN KEY (assignee_id) REFERENCES users (id) ON DELETE SET NULL,
  CONSTRAINT fk_br_reported FOREIGN KEY (reported_user_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Canned responses (macros) for staff replies.
CREATE TABLE IF NOT EXISTS bug_report_macros (
  id         INT UNSIGNED NOT NULL AUTO_INCREMENT,
  title      VARCHAR(80)  NOT NULL,
  body       TEXT         NOT NULL,
  created_by INT UNSIGNED NULL,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT fk_brmac_creator FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- visibility 'internal' = staff-only private note (never shown to the reporter).
-- is_system = 1 for generated events (label/status changes). author_id NULL = anon or system.
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

-- Participants drive access control and the red "Reports" nav link.
-- role: reporter (opener), collaborator (invited user), staff (assigned/invited staff).
CREATE TABLE IF NOT EXISTS bug_report_participants (
  report_id  INT UNSIGNED NOT NULL,
  user_id    INT UNSIGNED NOT NULL,
  role       ENUM('reporter','collaborator','staff') NOT NULL DEFAULT 'collaborator',
  added_by   INT UNSIGNED NULL,
  last_seen_at DATETIME   NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
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

-- Migration history baseline (regenerate with: go run ./cmd/migrate -dump-seed)
-- A fresh install is current, so every migrate.sql section is recorded as
-- applied. The migrate tool reads this to know what is already in place.
CREATE TABLE IF NOT EXISTS schema_migrations (
  section    INT UNSIGNED NOT NULL,
  name       VARCHAR(160) NOT NULL DEFAULT '',
  applied_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (section)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO schema_migrations (section, name) VALUES
  (1, 'Role expansion + disabled flag'),
  (2, 'Trainer profile'),
  (3, 'Granular location fields'),
  (4, 'Pronouns'),
  (5, 'Directory hidden flag'),
  (6, 'Raid ban flag'),
  (7, 'Favourite Pokemon'),
  (8, 'User strikes'),
  (9, 'Legacy raid finder tables (v1)'),
  (10, 'raid_joins status column'),
  (11, 'Invite token length (supports both old 64-char and new shorter codes)'),
  (12, 'Invite role assignment, multi-use codes, pending role confirmation'),
  (13, 'API access permission'),
  (14, 'Tag system'),
  (15, 'Raid XP and activity tracking'),
  (16, 'User language preference'),
  (17, 'Store system'),
  (18, 'Boss tier on raid posts'),
  (19, 'Custom tag cooldown + color rate limiting'),
  (20, 'Account suspension reason'),
  (21, 'Translator permission + translation edits'),
  (22, 'Raid Finder v2: lobbies, trust events, awards'),
  (23, 'Locale registry'),
  (24, 'Host unfulfilled trust event (2026-06-11)'),
  (25, 'Translator applications (2026-06-11)'),
  (26, 'Shiny collection privacy flag (2026-06-18)'),
  (27, 'Friends and blocks (2026-06-22)'),
  (28, 'Feedback options -- staff-curated list of Pokemon-themed phrases (2026-06-22)'),
  (29, 'User feedback -- one review per author/target pair; updatable via ON DUPLICATE KEY (2026-06-22)'),
  (30, 'Mobile companion app tables (2026-06-22)'),
  (31, 'Event Pokémon support in shiny collection (2026-06-23)'),
  (32, 'Trainer level field (2026-06-23)'),
  (33, 'Drop unique constraint on user_shinies to allow true duplicate shiny entries (2026-06-23)'),
  (34, 'Rename user_friends to user_follows (2026-06-23)'),
  (35, 'Confirm timeout warning flag on raid_lobby_members (2026-06-25)'),
  (36, 'Per-award minimum grant rank (2026-06-25)'),
  (37, 'Trainer avatar sprite locks (2026-06-26)'),
  (38, 'Shiny evolved_at (2026-06-26)'),
  (39, 'Bug report system "Report Me Not" (2026-06-28)'),
  (40, 'Bug reports triage enhancements (2026-06-28)'),
  (41, 'Player ("bad actor") report system (2026-06-28)'),
  (42, 'Transactional email: email_verified_at + email_tokens (2026-07-03)'),
  (43, 'Regional form support in shiny collection (2026-07-05)');

-- After first deploy: register your admin account via the UI, then run:
--   UPDATE users SET role = 'admin' WHERE username = 'yourusername';
