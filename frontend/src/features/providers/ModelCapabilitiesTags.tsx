import React from 'react';
import { Tag, Tooltip, Typography } from 'antd';
import {
  MessageOutlined,
  ThunderboltOutlined,
  PictureOutlined,
  ToolOutlined,
  BulbOutlined,
  ApiOutlined,
  FileImageOutlined,
  AudioOutlined,
  VideoCameraOutlined,
} from '@ant-design/icons';
import { useI18n } from '../../i18n';

const { Text } = Typography;

export interface ModelCapabilitiesProps {
  capabilities?: Record<string, any>;
  modelName?: string;
  compact?: boolean;
}

export const ModelCapabilitiesTags: React.FC<ModelCapabilitiesProps> = ({
  capabilities,
  modelName,
  compact = false,
}) => {
  const { t } = useI18n();

  const lower = (modelName || '').toLowerCase();
  const caps = capabilities || {};
  const hasCaps = capabilities && Object.keys(capabilities).length > 0;

  // Modalities & Endpoints
  const endpoints: string[] = Array.isArray(caps.endpoints) ? caps.endpoints : [];
  const modalities: string[] = Array.isArray(caps.modalities) ? caps.modalities : [];

  // Embedding check
  const isEmbedding =
    endpoints.some(e => e.includes('embedding')) || (!hasCaps && lower.includes('embed'));

  // Image Gen check
  const isImageGen =
    endpoints.some(e => e.includes('images/generations')) ||
    (!hasCaps &&
      (lower.includes('dall-e') ||
        lower.includes('flux') ||
        lower.includes('midjourney') ||
        lower.includes('stable-diffusion') ||
        lower.includes('sdxl')));

  // Video Gen check
  const isVideoGen =
    endpoints.some(e => e.includes('videos/generations')) ||
    (!hasCaps &&
      (lower.includes('sora') ||
        lower.includes('kling') ||
        lower.includes('cogvideox') ||
        lower.includes('runway') ||
        lower.includes('luma') ||
        lower.includes('pika') ||
        lower.includes('vidu') ||
        lower.includes('wanx') ||
        lower.includes('hailuo') ||
        lower.includes('minimax-video') ||
        lower.includes('hunyuan-video') ||
        lower.includes('mochi')));

  // Video Understanding check
  const isVideoUnderstanding =
    Boolean(caps.video) ||
    modalities.includes('video') ||
    (!hasCaps &&
      (lower.includes('gemini-1.5') ||
        lower.includes('gemini-2.0') ||
        lower.includes('gemini-exp') ||
        lower.includes('qwen2-vl') ||
        lower.includes('qwen2.5-vl') ||
        lower.includes('internvl2') ||
        lower.includes('cogvlm2-video') ||
        lower.includes('video-llava') ||
        lower.includes('videollama') ||
        (lower.includes('video') && !lower.includes('text'))));

  // Audio check
  const isAudio =
    modalities.includes('audio') ||
    endpoints.some(e => e.includes('audio')) ||
    (!hasCaps && (lower.includes('whisper') || lower.includes('tts') || lower.includes('audio')));

  // Chat check
  const isChat =
    !isEmbedding &&
    !isImageGen &&
    !isVideoGen &&
    (endpoints.some(e => e.includes('chat') || e.includes('messages')) ||
      endpoints.length === 0 ||
      (!hasCaps && !isAudio));

  // Stream check
  const isStream =
    caps.stream !== undefined
      ? Boolean(caps.stream)
      : !isEmbedding && !isImageGen && !isVideoGen && (hasCaps ? false : true);

  // Vision / Image check (P0 requirement)
  const isVision =
    Boolean(caps.vision) ||
    modalities.includes('image') ||
    (!hasCaps &&
      (lower.includes('vision') ||
        lower.includes('vl') ||
        lower.includes('4o') ||
        lower.includes('gemini') ||
        lower.includes('claude-3') ||
        lower.includes('internvl') ||
        lower.includes('glm-5') ||
        lower.includes('glm-4v') ||
        lower.includes('glm-4.5v') ||
        lower.includes('qwenvl') ||
        lower.includes('qwen-vl') ||
        lower.includes('minicpm-v') ||
        lower.includes('pixtral') ||
        lower.includes('llava') ||
        lower.includes('omni')));

  // Tools / Function calling
  const hasTools =
    caps.tools !== undefined
      ? Boolean(caps.tools)
      : !isEmbedding && !isImageGen && !isVideoGen && !isAudio && (hasCaps ? false : true);

  // Reasoning / Deep thinking
  const isReasoning =
    Boolean(caps.reasoning) ||
    (!hasCaps &&
      (lower.includes('r1') ||
        lower.startsWith('o1') ||
        lower.includes('-o1') ||
        lower.startsWith('o3') ||
        lower.includes('-o3') ||
        lower.includes('qwq') ||
        lower.includes('reasoning') ||
        lower.includes('thinking')));

  // Max input tokens
  const maxTokens = Number(
    caps.max_input_tokens ||
      (!hasCaps
        ? lower.includes('gemini')
          ? 1000000
          : lower.includes('claude-3')
            ? 200000
            : lower.includes('glm-5')
              ? 200000
              : lower.includes('4o')
                ? 128000
                : lower.includes('deepseek')
                  ? 64000
                  : 0
        : 0),
  );

  const tags: React.ReactNode[] = [];

  const createEndpointTooltip = (eps: string[]) => {
    if (!eps || eps.length === 0) return null;
    return (
      <div>
        <div>{t('providers.cap_endpoints_tip', 'Supported Endpoints')}</div>
        <ul style={{ margin: '4px 0 0 16px', padding: 0 }}>
          {eps.map(ep => (
            <li key={ep}>
              <code>{ep}</code>
            </li>
          ))}
        </ul>
      </div>
    );
  };

  // 1. Interface / Modality tag
  if (isChat) {
    const chatEps = endpoints.filter(
      e => e.includes('chat') || e.includes('messages') || e.includes('completions'),
    );
    const effectiveChatEps = chatEps.length > 0 ? chatEps : endpoints;
    const chatTooltip = createEndpointTooltip(effectiveChatEps);
    const tag = (
      <Tag
        key="chat"
        color="blue"
        icon={<MessageOutlined />}
        style={{ margin: 0, padding: '0 5px' }}
      >
        {t('providers.cap_chat', 'Chat')}
      </Tag>
    );
    tags.push(
      chatTooltip ? (
        <Tooltip key="chat" title={chatTooltip}>
          {tag}
        </Tooltip>
      ) : (
        tag
      ),
    );
  }
  if (isEmbedding) {
    const embEps = endpoints.filter(e => e.includes('embedding'));
    const effectiveEmbEps = embEps.length > 0 ? embEps : endpoints;
    const embTooltip = createEndpointTooltip(effectiveEmbEps);
    const tag = (
      <Tag
        key="embedding"
        color="cyan"
        icon={<ApiOutlined />}
        style={{ margin: 0, padding: '0 5px' }}
      >
        {t('providers.cap_embedding', 'Embeddings')}
      </Tag>
    );
    tags.push(
      embTooltip ? (
        <Tooltip key="embedding" title={embTooltip}>
          {tag}
        </Tooltip>
      ) : (
        tag
      ),
    );
  }
  if (isImageGen) {
    const imgEps = endpoints.filter(e => e.includes('image') || e.includes('dall-e'));
    const effectiveImgEps = imgEps.length > 0 ? imgEps : endpoints;
    const imgTooltip = createEndpointTooltip(effectiveImgEps);
    const tag = (
      <Tag
        key="image_gen"
        color="cyan"
        icon={<FileImageOutlined />}
        style={{ margin: 0, padding: '0 5px' }}
      >
        {t('providers.cap_image_gen', 'Image Gen')}
      </Tag>
    );
    tags.push(
      imgTooltip ? (
        <Tooltip key="image_gen" title={imgTooltip}>
          {tag}
        </Tooltip>
      ) : (
        tag
      ),
    );
  }
  if (isVideoGen) {
    const videoEps = endpoints.filter(e => e.includes('video'));
    const effectiveVideoEps = videoEps.length > 0 ? videoEps : endpoints;
    const videoTooltip =
      createEndpointTooltip(effectiveVideoEps) ||
      t(
        'providers.cap_video_gen_tip',
        'Multimodal: Supports text-to-video / image-to-video generation',
      );
    const tag = (
      <Tag
        key="video_gen"
        color="magenta"
        icon={<VideoCameraOutlined />}
        style={{ margin: 0, padding: '0 5px' }}
      >
        {t('providers.cap_video_gen', 'Video Gen')}
      </Tag>
    );
    tags.push(
      videoTooltip ? (
        <Tooltip key="video_gen" title={videoTooltip}>
          {tag}
        </Tooltip>
      ) : (
        tag
      ),
    );
  }
  if (isAudio) {
    const audioEps = endpoints.filter(
      e => e.includes('audio') || e.includes('speech') || e.includes('transcription'),
    );
    const effectiveAudioEps = audioEps.length > 0 ? audioEps : endpoints;
    const audioTooltip = createEndpointTooltip(effectiveAudioEps);
    const tag = (
      <Tag
        key="audio"
        color="green"
        icon={<AudioOutlined />}
        style={{ margin: 0, padding: '0 5px' }}
      >
        {t('providers.cap_audio', 'Audio')}
      </Tag>
    );
    tags.push(
      audioTooltip ? (
        <Tooltip key="audio" title={audioTooltip}>
          {tag}
        </Tooltip>
      ) : (
        tag
      ),
    );
  }

  // Fallback: if endpoints exist but no standard interface tag was matched
  const hasInterfaceTag = isChat || isEmbedding || isImageGen || isVideoGen || isAudio;
  if (!hasInterfaceTag && endpoints.length > 0) {
    tags.push(
      <Tooltip key="endpoints" title={createEndpointTooltip(endpoints)}>
        <Tag color="blue" icon={<ApiOutlined />} style={{ margin: 0, padding: '0 5px' }}>
          {t('providers.cap_endpoints_tip', 'Supported Endpoints')}
        </Tag>
      </Tooltip>,
    );
  }

  // 2. Stream tag (P0 requirement)
  if (isStream) {
    tags.push(
      <Tag
        key="stream"
        color="gold"
        icon={<ThunderboltOutlined />}
        style={{ margin: 0, padding: '0 5px' }}
      >
        {t('providers.cap_stream', 'Stream')}
      </Tag>,
    );
  }

  // 3. Multimodal: Vision / Image tag (P0 requirement)
  if (isVision) {
    tags.push(
      <Tooltip
        key="vision"
        title={t(
          'providers.cap_vision_tip',
          'Multimodal: Supports image input & visual comprehension',
        )}
      >
        <Tag color="purple" icon={<PictureOutlined />} style={{ margin: 0, padding: '0 5px' }}>
          {t('providers.cap_vision', 'Vision')}
        </Tag>
      </Tooltip>,
    );
  }

  // 4. Multimodal: Video understanding tag
  if (isVideoUnderstanding) {
    tags.push(
      <Tooltip
        key="video"
        title={t(
          'providers.cap_video_tip',
          'Multimodal: Supports video input & multimodal analysis',
        )}
      >
        <Tag color="volcano" icon={<VideoCameraOutlined />} style={{ margin: 0, padding: '0 5px' }}>
          {t('providers.cap_video', 'Video')}
        </Tag>
      </Tooltip>,
    );
  }

  // 5. Function calling / Tools
  if (hasTools && !compact) {
    tags.push(
      <Tag
        key="tools"
        color="geekblue"
        icon={<ToolOutlined />}
        style={{ margin: 0, padding: '0 5px' }}
      >
        {t('providers.cap_tools', 'Tools')}
      </Tag>,
    );
  }

  // 6. Reasoning
  if (isReasoning) {
    tags.push(
      <Tag
        key="reasoning"
        color="volcano"
        icon={<BulbOutlined />}
        style={{ margin: 0, padding: '0 5px' }}
      >
        {t('providers.cap_reasoning', 'Reasoning')}
      </Tag>,
    );
  }

  // 7. Context Window
  if (maxTokens > 0) {
    const formatted = maxTokens >= 1000 ? `${(maxTokens / 1000).toFixed(0)}k` : `${maxTokens}`;
    tags.push(
      <Tooltip
        key="ctx"
        title={`${t('providers.max_tokens_tooltip', 'Maximum input context:')} ${maxTokens.toLocaleString()} tokens`}
      >
        <Tag style={{ margin: 0, padding: '0 4px', cursor: 'help' }}>{formatted}</Tag>
      </Tooltip>,
    );
  }

  if (tags.length === 0) {
    return <Text type="secondary">{t('providers.standard_capability', 'Standard')}</Text>;
  }

  return (
    <div style={{ display: 'inline-flex', flexWrap: 'nowrap', gap: 4, alignItems: 'center' }}>
      {tags}
    </div>
  );
};
