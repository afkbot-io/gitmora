import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { safeExternalURL } from "../../domain/context.js";

/** Raw HTML stays text. Source images never trigger network requests or tracking. */
export function MessageContent({ text }: { text: string }) {
  return (
    <div className="message-prose min-w-0 max-w-[80ch] text-sm leading-7 wrap-anywhere [&_p]:my-3 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0 [&_h1]:text-xl [&_h2]:text-lg [&_h3]:text-base [&_h4]:text-sm [&_h1]:mt-5 [&_h2]:mt-5 [&_h3]:mt-4 [&_li]:my-1 [&_ul]:pl-5 [&_ol]:pl-5 [&_blockquote]:mx-0 [&_blockquote]:border-l-2 [&_blockquote]:border-control [&_blockquote]:pl-4 [&_blockquote]:text-muted [&_pre]:max-w-full [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:border [&_pre]:border-line [&_pre]:bg-canvas [&_pre]:p-4 [&_pre]:text-xs [&_pre]:leading-6 [&_code]:font-mono [&_code]:text-[0.85em] [&_hr]:my-5 [&_hr]:border-line [&_table]:w-full [&_table]:border-collapse [&_th]:border [&_th]:border-line [&_th]:p-2 [&_th]:text-left [&_td]:border [&_td]:border-line [&_td]:p-2">
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          h1: ({ children }) => (
            <h3 className="mt-5 text-lg font-semibold">{children}</h3>
          ),
          h2: ({ children }) => (
            <h3 className="mt-5 text-base font-semibold">{children}</h3>
          ),
          h3: ({ children }) => (
            <h4 className="mt-4 text-sm font-semibold">{children}</h4>
          ),
          h4: ({ children }) => (
            <h4 className="mt-4 text-sm font-semibold">{children}</h4>
          ),
          h5: ({ children }) => (
            <h4 className="mt-4 text-sm font-medium">{children}</h4>
          ),
          h6: ({ children }) => (
            <h4 className="mt-4 text-sm font-medium">{children}</h4>
          ),
          a: ({ href, children }) => {
            const url = safeExternalURL(href ?? "");
            return url ? (
              <a href={url} target="_blank" rel="noopener noreferrer">
                {children}
              </a>
            ) : (
              <span>{children}</span>
            );
          },
          img: ({ alt }) => (
            <span className="text-muted">[{alt || "image"}]</span>
          ),
          table: ({ children }) => (
            <div className="my-3 max-w-full overflow-x-auto" tabIndex={0}>
              <table>{children}</table>
            </div>
          ),
          pre: ({ children }) => <pre tabIndex={0}>{children}</pre>,
        }}
      >
        {text}
      </Markdown>
    </div>
  );
}
