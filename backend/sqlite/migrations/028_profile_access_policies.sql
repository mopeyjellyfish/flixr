CREATE TABLE profile_access_policies (
 profile_id TEXT PRIMARY KEY REFERENCES profiles(id) ON DELETE CASCADE,
 library_ids_json TEXT NOT NULL DEFAULT '[]',
 rating_region TEXT NOT NULL DEFAULT '',
 max_rating TEXT NOT NULL DEFAULT '',
 unrated_policy TEXT NOT NULL DEFAULT 'allow' CHECK(unrated_policy IN ('allow','deny')),
 allow_tags_json TEXT NOT NULL DEFAULT '[]',
 deny_tags_json TEXT NOT NULL DEFAULT '[]',
 version INTEGER NOT NULL DEFAULT 1 CHECK(version > 0)
);
INSERT INTO profile_access_policies(profile_id) SELECT id FROM profiles;
CREATE TRIGGER profile_access_policy_insert AFTER INSERT ON profiles BEGIN
 INSERT INTO profile_access_policies(profile_id) VALUES(NEW.id);
END;
