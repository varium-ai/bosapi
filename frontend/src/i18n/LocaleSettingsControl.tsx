import React from 'react';
import { Dropdown, Button, Space, Typography, Tooltip } from 'antd';
import { TranslationOutlined, FieldTimeOutlined, CheckOutlined } from '@ant-design/icons';
import { useI18n } from './I18nContext';
import { TIMEZONE_OPTIONS, getTimezoneLabel } from './formatters';
import { LANGUAGE_OPTIONS } from './languages';
import { type SupportedTimezone } from './types';

const { Text } = Typography;

interface LocaleSettingsControlProps {
  compact?: boolean;
  languageOnly?: boolean;
}

export const LocaleSettingsControl: React.FC<LocaleSettingsControlProps> = ({
  compact = false,
  languageOnly = false,
}) => {
  const { language, setLanguage, timezone, setTimezone, timezoneBadge, t } = useI18n();

  const langMenuItems = LANGUAGE_OPTIONS.map(option => ({
    key: option.value,
    label: (
      <Space style={{ width: '100%', justifyContent: 'space-between' }}>
        <span>{t(option.labelKey)}</span>
        {language === option.value && <CheckOutlined style={{ color: 'var(--ink)' }} />}
      </Space>
    ),
    onClick: () => setLanguage(option.value),
  }));

  const tzMenuItems = TIMEZONE_OPTIONS.map(item => ({
    key: item.value,
    label: (
      <Space style={{ width: '100%', justifyContent: 'space-between', minWidth: 200 }}>
        <span>{getTimezoneLabel(item, language)}</span>
        {timezone === item.value && <CheckOutlined style={{ color: 'var(--ink)' }} />}
      </Space>
    ),
    onClick: () => setTimezone(item.value as SupportedTimezone),
  }));

  return (
    <Space size={8} align="center">
      {/* Timezone Selector */}
      {!languageOnly && (
        <Tooltip title={t('nav.timezone_desc', 'Timezone for logs, billing, and report periods')}>
          <Dropdown menu={{ items: tzMenuItems }} placement="bottomRight" trigger={['click']}>
            <Button
              size="small"
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 4,
                fontSize: 12,
                height: 28,
                padding: '0 8px',
                borderRadius: 6,
                color: '#404040',
                borderColor: '#e5e5e5',
              }}
            >
              <FieldTimeOutlined style={{ fontSize: 13, color: '#171717' }} />
              <Text style={{ fontSize: 12, fontWeight: 500 }}>
                {compact ? timezoneBadge : `${timezoneBadge}`}
              </Text>
            </Button>
          </Dropdown>
        </Tooltip>
      )}

      {/* Language Switcher */}
      <Dropdown menu={{ items: langMenuItems }} placement="bottomRight" trigger={['click']}>
        <Button
          aria-label={t('nav.language')}
          size="small"
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 4,
            fontSize: 12,
            height: 28,
            padding: '0 8px',
            borderRadius: 6,
            color: '#404040',
            borderColor: '#e5e5e5',
          }}
        >
          <TranslationOutlined style={{ fontSize: 13, color: '#171717' }} />
          <Text style={{ fontSize: 12, fontWeight: 500 }}>
            {LANGUAGE_OPTIONS.find(option => option.value === language)?.shortLabel}
          </Text>
        </Button>
      </Dropdown>
    </Space>
  );
};
