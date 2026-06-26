-- hailsdotgo migrate.sql -- upgrade path for existing installs
-- New installs: run schema.sql only. Do not run this file on a fresh database.
--
-- Apply each numbered section you have not yet run, in order.
-- CREATE TABLE IF NOT EXISTS and INSERT IGNORE lines are safe to re-run.
-- ALTER TABLE and MODIFY COLUMN lines will error if already applied --
-- skip them once done, or check your schema first.

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

