PRAGMA foreign_keys=ON;

INSERT INTO owner(id,password_hash,salt) VALUES(1,x'01020304',x'05060708');
INSERT INTO profiles(id,name,pin_hash,salt,attempts,locked_until)
VALUES('profile-upgrade','Upgrade Viewer',x'11121314',x'15161718',2,123456789);
INSERT INTO sessions(token_hash,subject,expires_at,revoked)
VALUES(x'21222324','profile:profile-upgrade',2000000000,0);

INSERT INTO settings(key,value) VALUES
 ('film_root','/media/films'),
 ('tv_root','/media/tv'),
 ('metadata_language','ja-JP'),
 ('metadata_region','JP');

INSERT INTO catalog_items(id,kind,title,relative_path,root_kind,duration_ms)
VALUES('film-upgrade','film','Preserved Film','Preserved Film.mp4','film',7200000);
INSERT INTO profile_view_preferences(profile_id,media,view_mode,sort_mode)
VALUES('profile-upgrade','all','grid','year');
INSERT INTO profile_film_list(profile_id,catalog_id,added_at)
VALUES('profile-upgrade','film-upgrade',1700000000);
INSERT INTO profile_audio_preferences(profile_id,language)
VALUES('profile-upgrade','jpn');
INSERT INTO profile_subtitle_preferences(profile_id,mode,language,prefer_sdh)
VALUES('profile-upgrade','automatic','eng',1);
INSERT INTO progress(profile_id,catalog_id,position_ms,updated_at,completed,completed_at,generation,observation,completion_id)
VALUES('profile-upgrade','film-upgrade',3456000,1700000100,0,0,7,12,'');
INSERT INTO profile_ratings(profile_id,catalog_id,rating,provenance,source_id,updated_at)
VALUES('profile-upgrade','film-upgrade',5,'local','',1700000200);
INSERT INTO viewing_events(event_id,profile_id,catalog_id,title,kind,event_type,provenance,source_id,source_time,recorded_at)
VALUES('event-upgrade','profile-upgrade','film-upgrade','Preserved Film','film','summary','local','',1700000300,1700000300);

UPDATE libraries SET created_at=100 WHERE id='films';
UPDATE libraries SET created_at=300 WHERE id='tv';
INSERT INTO libraries(id,name,kind,created_at) VALUES('archive','Archive','film',200);
INSERT INTO library_locations(id,library_id,root_path,state,scan_complete,item_count,updated_at)
VALUES('archive-root','archive','/media/archive','available',1,1,1700000400);
INSERT INTO library_scan_policies(library_id,enabled,schedule_kind,interval_seconds,local_time,timezone,updated_at)
VALUES('archive',1,'daily',86400,'04:30','Europe/London',1700000500);
