import Markdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

export function AnnouncementMarkdown({ body }: { body: string }) {
  return (
    <div className="announcement-markdown">
      <Markdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          a: ({ children, href }) => (
            <a href={href} target="_blank" rel="noopener noreferrer">
              {children}
            </a>
          ),
        }}
      >
        {body}
      </Markdown>
    </div>
  );
}
