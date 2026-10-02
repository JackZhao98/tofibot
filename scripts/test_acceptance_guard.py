import unittest
from unittest.mock import patch
from acceptance import AcceptanceClient

class GuardTest(unittest.TestCase):
    def test_rejects_personal_or_replaced_instance_before_mutation(self):
        for health in ({'environment':'personal','instance_id':'expected'},
                       {'environment':'acceptance','instance_id':'different'}):
            with self.subTest(health=health):
                c = AcceptanceClient('http://127.0.0.1:1','expected')
                with patch('acceptance.json.load',return_value=health), patch.object(c.client,'open') as opened:
                    with self.assertRaises(RuntimeError):
                        c.call('POST','/api/bots',{'name':'should not be sent'})
                    self.assertEqual(opened.call_count,1)

if __name__ == '__main__':
    unittest.main()
