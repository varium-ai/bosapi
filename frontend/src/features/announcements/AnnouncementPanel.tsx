import { useState } from 'react';
import { Alert, Button, Card, Empty, Pagination, Skeleton, Tag } from 'antd';
import { useI18n } from '../../i18n';
import { AnnouncementMarkdown } from './AnnouncementMarkdown';
import { announcementLoadErrorKey, useAnnouncements } from './data';
import './announcements.css';

export function AnnouncementPanel({
  scope,
  keyPortal = false,
}: {
  scope: string;
  keyPortal?: boolean;
}) {
  const { t, formatDateTime } = useI18n();
  const [page, setPage] = useState(1);
  const query = useAnnouncements(scope, page, keyPortal);
  return (
    <Card className="announcement-panel" title={t('announcements.title')}>
      {query.isError && (
        <Alert
          type="warning"
          showIcon
          message={t(announcementLoadErrorKey(query.error))}
          action={
            <Button size="small" onClick={() => query.refetch()}>
              {t('common.retry')}
            </Button>
          }
        />
      )}
      {query.isLoading ? (
        <Skeleton active paragraph={{ rows: 5 }} />
      ) : (
        query.data && (
          <>
            {query.data.items.length ? (
              query.data.items.map(item => (
                <article className="announcement-item" key={item.id}>
                  <div className="announcement-heading">
                    <h3>{item.title}</h3>
                  </div>
                  <Tag>
                    {t(
                      item.organization_id
                        ? 'announcements.scope_organization'
                        : 'announcements.scope_global',
                    )}
                  </Tag>
                  <time dateTime={item.created_at}>{formatDateTime(item.created_at)}</time>
                  <AnnouncementMarkdown body={item.body} />
                </article>
              ))
            ) : !query.isError ? (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('announcements.empty')} />
            ) : null}
            {query.data.total > 5 && (
              <Pagination
                size="small"
                current={page}
                pageSize={5}
                total={query.data.total}
                showSizeChanger={false}
                onChange={setPage}
              />
            )}
          </>
        )
      )}
    </Card>
  );
}
