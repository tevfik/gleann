"""A small Python application."""

from util import clean, join_words


class Registry:
    def __init__(self):
        self.items = {}

    def add(self, name, value):
        self.items[clean(name)] = value

    def describe(self):
        return join_words(self.items.keys())


def build_registry(names):
    reg = Registry()
    for n in names:
        reg.add(n, len(n))
    return reg


def main():
    reg = build_registry(["Alpha", "Beta"])
    print(reg.describe())


main()
