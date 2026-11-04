"""Unit checks for the test-only HTTP acceptance oracle."""
import unittest
from phase4_php_nginx_lab import verify_response


class ResponseTests(unittest.TestCase):
    def test_php_source_is_not_a_successful_response(self):
        with self.assertRaises(AssertionError):
            verify_response("/index.php", 200, b"<?php echo 'executed';", b"executed")

    def test_missing_script_must_be_404(self):
        with self.assertRaises(AssertionError):
            verify_response("/missing.php", 200, b"executed", None)

    def test_good_responses(self):
        verify_response("/index.php", 200, b"executed", b"executed")
        verify_response("/missing.php", 404, b"not found", None)


if __name__ == "__main__":
    unittest.main()
