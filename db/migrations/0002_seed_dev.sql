-- Dev seed: one tenant + one GitHub App installation mapping, so the
-- walking skeleton resolves a tenant for the sample webhook out of the box.
-- The sample payload (scripts/sample-pull-request.json) uses installation.id = 42424242.

INSERT INTO tenant (tenant_id, name)
VALUES ('11111111-1111-1111-1111-111111111111', 'Acme Dev Org')
ON CONFLICT DO NOTHING;

INSERT INTO github_installation (installation_id, tenant_id)
VALUES (42424242, '11111111-1111-1111-1111-111111111111')
ON CONFLICT DO NOTHING;

-- A second tenant used by the red-team isolation test.
INSERT INTO tenant (tenant_id, name)
VALUES ('22222222-2222-2222-2222-222222222222', 'Globex Dev Org')
ON CONFLICT DO NOTHING;

INSERT INTO github_installation (installation_id, tenant_id)
VALUES (84848484, '22222222-2222-2222-2222-222222222222')
ON CONFLICT DO NOTHING;
