import { Button } from 'antd';
import { LeftOutlined, RightOutlined } from '@ant-design/icons';
import type { ReactNode } from 'react';
import { useI18n } from '../../i18n';

export function CursorPagination({
  page,
  hasNext,
  loading,
  onPrevious,
  onNext,
  pageNumbers,
  onPageChange,
  children,
}: {
  page: number;
  hasNext: boolean;
  loading?: boolean;
  onPrevious: () => void;
  onNext: () => void;
  pageNumbers?: number[];
  onPageChange?: (page: number) => void;
  children?: ReactNode;
}) {
  const { t, formatNumber } = useI18n();
  return (
    <div className="console-cursor-pagination">
      {children}
      <Button icon={<LeftOutlined />} disabled={loading || page <= 1} onClick={onPrevious}>
        {t('keys.detail_previous')}
      </Button>
      {pageNumbers?.length && onPageChange ? (
        pageNumbers.map(pageNumber => (
          <Button
            key={pageNumber}
            type={pageNumber === page ? 'primary' : 'text'}
            disabled={loading || pageNumber === page}
            aria-current={pageNumber === page ? 'page' : undefined}
            className="console-page-number"
            onClick={() => onPageChange(pageNumber)}
          >
            {formatNumber(pageNumber)}
          </Button>
        ))
      ) : (
        <span aria-current="page" className="console-page-number">
          {formatNumber(page)}
        </span>
      )}
      <Button disabled={loading || !hasNext} onClick={onNext}>
        {t('keys.detail_next')}
        <RightOutlined />
      </Button>
    </div>
  );
}
