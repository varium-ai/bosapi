// Loopback-only fixtures for checking publication, editing, deletion and both overview layouts.
export function createAnnouncementPreview() {
  let items = [
    {
      id: 1,
      organization_id: 0,
      title: 'BOS API release notes',
      body: '## Welcome\n\nAnnouncements are available to **all signed-in users**.\n\n- Administrators\n- Organization administrators\n- API Key users\n\n| Feature | Status |\n| --- | --- |\n| Markdown | Available |\n\n```sh\necho "Hello, BOS API"\n```',
      created_at: '2026-10-30T08:00:00Z',
    },
    {
      id: 2,
      organization_id: 2,
      title: 'Private organization 2 announcement',
      body: 'Visible only to organization 2.',
      created_at: '2026-10-30T08:01:00Z',
    },
  ];
  let nextID = 3;
  return async (req, res, url) => {
    const match = url.pathname.match(
      /^\/api\/v1\/(admin|key-portal)\/announcements(?:\/(manage|\d+))?$/,
    );
    if (!match) return false;
    const respond = (status, data) => {
      res.statusCode = status;
      res.end(JSON.stringify({ data }));
      return true;
    };
    const token = req.headers.authorization;
    const isAdmin = token === 'Bearer ui-preview-admin';
    const isOrg = token === 'Bearer ui-preview-org';
    const isKey = token === 'Bearer ui-preview-key';
    if (!isAdmin && !isOrg && !isKey) return respond(401, null);
    const organizationID = isAdmin ? 0 : 1;
    const management = match[2] === 'manage';
    if ((management || req.method !== 'GET') && (match[1] !== 'admin' || (!isAdmin && !isOrg)))
      return respond(403, null);
    if (req.method === 'GET') {
      const visible = items.filter(
        item =>
          item.organization_id === organizationID || (!management && item.organization_id === 0),
      );
      const page = Number(url.searchParams.get('page') || 1);
      const size = Number(url.searchParams.get('page_size') || 5);
      return respond(200, {
        items: visible.slice((page - 1) * size, page * size),
        total: visible.length,
      });
    }
    if (req.method === 'POST') {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const publication = JSON.parse(Buffer.concat(chunks).toString());
      const item = {
        id: nextID++,
        title: publication.title,
        body: publication.body,
        organization_id: organizationID,
        created_at: new Date().toISOString(),
      };
      items.unshift(item);
      return respond(201, item);
    }
    const item = items.find(
      item => item.id === Number(match[2]) && item.organization_id === organizationID,
    );
    if (!item) return respond(404, null);
    if (req.method === 'PUT') {
      const chunks = [];
      for await (const chunk of req) chunks.push(chunk);
      const publication = JSON.parse(Buffer.concat(chunks).toString());
      item.title = publication.title;
      item.body = publication.body;
      return respond(200, item);
    }
    if (req.method === 'DELETE') {
      items = items.filter(item => item.id !== Number(match[2]));
      return respond(200, { deleted: true });
    }
    return false;
  };
}
