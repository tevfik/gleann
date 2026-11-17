"""Helpers shared by the Python application."""


def clean(text):
    return text.strip().lower()


def join_words(words):
    return " ".join(clean(w) for w in words)
