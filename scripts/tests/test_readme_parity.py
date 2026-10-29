"""README.md and README.en.md must say the same thing (#129): same structure and the same
commands, variables, URLs and image names. Prose is translated; these anchors are not."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[2]


def load(name):
    return (ROOT / name).read_text(encoding='utf-8')


def code_blocks(text):
    blocks = re.findall(r'```[a-z]+\n(.*?)```', text, re.S)
    # Shell comments are translated; commands are not. Unlabeled fences (the diagram) are skipped.
    return [[l for l in b.splitlines() if l.strip() and not l.lstrip().startswith('#')
             and not re.search(r'\s#\s', l) and '│' not in l and '├' not in l and '└' not in l
             and '▼' not in l] for b in blocks]


def outside_blocks(text):
    return re.sub(r'```.*?```', '', text, flags=re.S)


class ReadmeParityTests(unittest.TestCase):
    zh = load('README.md')
    en = load('README.en.md')

    def test_same_sections(self):
        count = lambda t: len(re.findall(r'^##+ ', t, re.M))
        self.assertEqual(count(self.zh), count(self.en))

    def test_same_commands(self):
        self.assertEqual(code_blocks(self.zh), code_blocks(self.en))

    def test_same_identifiers(self):
        def tokens(text):
            body = outside_blocks(text)
            found = {t.split('=')[0] for t in re.findall(r'`([^`\n]+)`', body)}
            return {t for t in found if re.fullmatch(r'[A-Z][A-Z0-9_<>]+(=[\w./:-]+)?|[\w./-]+:[\w.-]+'
                                                      r'|/[\w./]+|[\w.-]+_[\w.-]+', t)
                    or t.startswith(('http://', 'art-ai'))}
        zh, en = tokens(self.zh), tokens(self.en)
        self.assertEqual(sorted(zh - en), [], 'in README.md only')
        self.assertEqual(sorted(en - zh), [], 'in README.en.md only')

    def test_same_urls_and_images(self):
        def urls(text):
            return sorted(set(re.findall(r'https?://[^\s)>`\'"]+', text)))
        self.assertEqual(urls(self.zh), urls(self.en))
        imgs = lambda t: sorted(re.findall(r'src="(docs/screenshots/[\w-]+?)(?:-zh|-en)?\.png"', t))
        self.assertEqual(imgs(self.zh), imgs(self.en))


if __name__ == '__main__':
    unittest.main()
